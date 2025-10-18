package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gin-replace-anon-struct/pkg/analyzer"
	"gin-replace-anon-struct/pkg/router"
	"gin-replace-anon-struct/pkg/transformer"

	"github.com/alecthomas/kong"
)

var CLI struct {
	WorkspaceRoot    string `kong:"arg,help='Workspace root directory to scan for handlers',type='existingdir'"`
	RouterFile       string `kong:"help='Router file to parse for routes and handler names',type='existingfile'"`
	DryRun           bool   `kong:"help='Show what would be changed without modifying files'"`
	Verbose          bool   `kong:"help='Enable verbose output'"`
	StripRoutePrefix string `kong:"help='Strip this prefix from route paths when generating @Tags (e.g., /api)'"`
}

func main() {
	kong.Parse(&CLI)

	if CLI.WorkspaceRoot == "" {
		log.Fatal("Please specify a workspace root directory")
	}

	if CLI.RouterFile == "" {
		log.Fatal("Please specify a router file")
	}

	// Process the workspace
	err := processWorkspace(CLI.WorkspaceRoot, CLI.RouterFile, CLI.StripRoutePrefix)
	if err != nil {
		log.Fatalf("Error processing workspace: %v", err)
	}
}

func processWorkspace(workspaceRoot, routerFile, stripRoutePrefix string) error {
	if CLI.Verbose {
		log.Printf("Processing workspace: %s", workspaceRoot)
		log.Printf("Using router file: %s", routerFile)
		if stripRoutePrefix != "" {
			log.Printf("Stripping route prefix: %s", stripRoutePrefix)
		}
	}

	// Step 1: Parse router file to get routes and handler names
	routes, err := parseRouterFile(routerFile, stripRoutePrefix)
	if err != nil {
		return fmt.Errorf("failed to parse router file: %w", err)
	}

	if CLI.Verbose {
		log.Printf("Found %d routes in router file", len(routes))
	}

	// Step 2: Extract unique handler names from routes
	handlerNames := extractHandlerNames(routes)
	if CLI.Verbose {
		log.Printf("Found %d unique handler names: %v", len(handlerNames), handlerNames)
	}

	// Step 3: Scan workspace for Go files containing these handlers
	handlerFiles, err := scanWorkspaceForHandlers(workspaceRoot, handlerNames)
	if err != nil {
		return fmt.Errorf("failed to scan workspace: %w", err)
	}

	if CLI.Verbose {
		log.Printf("Found %d files containing handlers", len(handlerFiles))
		for file, handlers := range handlerFiles {
			log.Printf("  %s: %v", file, handlers)
		}
	}

	// Step 4: Process each handler file
	totalStructs := 0
	for filePath, handlersInFile := range handlerFiles {
		structs, err := processHandlerFile(filePath, routerFile, stripRoutePrefix, handlersInFile)
		if err != nil {
			log.Printf("Error processing %s: %v", filePath, err)
			continue
		}
		totalStructs += structs
	}

	if CLI.Verbose {
		log.Printf("Total anonymous structs found: %d", totalStructs)
	}

	if totalStructs == 0 {
		log.Printf("No anonymous structs found in any handler files")
		return nil
	}

	return nil
}

// parseRouterFile parses the router file and extracts routes
func parseRouterFile(routerFile, stripRoutePrefix string) ([]router.Route, error) {
	parser := router.NewRouterParser(routerFile, stripRoutePrefix)
	return parser.ParseRouterFile()
}

// extractHandlerNames extracts unique handler names from routes
func extractHandlerNames(routes []router.Route) []string {
	handlerSet := make(map[string]bool)
	for _, route := range routes {
		if parts := strings.Split(route.Handler, "."); len(parts) == 2 {
			handlerSet[route.Handler] = true // Use full package.handler
		} else {
			handlerSet[route.Handler] = true
		}
	}

	var names []string
	for name := range handlerSet {
		names = append(names, name)
	}
	return names
}

// scanWorkspaceForHandlers scans the workspace for Go files containing the specified handlers
func scanWorkspaceForHandlers(workspaceRoot string, handlerNames []string) (map[string][]string, error) {
	handlerFiles := make(map[string][]string)

	// Walk through all Go files in the workspace
	err := filepath.Walk(workspaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-Go files
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip vendor directories and hidden files
		if strings.Contains(path, "vendor/") || strings.HasPrefix(filepath.Base(path), ".") {
			return nil
		}

		// Parse the Go file and look for handler functions
		handlers, err := findHandlersInFile(path, handlerNames)
		if err != nil {
			// Log error but continue processing other files
			if CLI.Verbose {
				log.Printf("Error parsing %s: %v", path, err)
			}
			return nil
		}

		if len(handlers) > 0 {
			handlerFiles[path] = handlers
		}

		return nil
	})

	return handlerFiles, err
}

// findHandlersInFile parses a Go file and returns which handler names it contains
func findHandlersInFile(filePath string, targetHandlers []string) ([]string, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	var foundHandlers []string
	targetSet := make(map[string]bool)
	for _, h := range targetHandlers {
		targetSet[h] = true
	}

	// Get the package name from the file
	packageName := file.Name.Name

	// Look for function declarations matching target handler names
	ast.Inspect(file, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok {
			// Check if packageName.handlerName is in the target set
			fullHandlerName := packageName + "." + fn.Name.Name
			if targetSet[fullHandlerName] {
				foundHandlers = append(foundHandlers, fn.Name.Name)
			}
		}
		return true
	})

	return foundHandlers, nil
}

// processHandlerFile processes a single handler file
func processHandlerFile(filePath, routerFile, stripRoutePrefix string, handlersInFile []string) (int, error) {
	if CLI.Verbose {
		log.Printf("Processing file: %s", filePath)
	}

	// Parse the file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, filePath, nil, parser.ParseComments)
	if err != nil {
		return 0, fmt.Errorf("failed to parse file: %w", err)
	}

	// Get package name
	packageName := file.Name.Name

	// Analyze anonymous structs with router context
	detector := analyzer.NewAnonymousStructDetector(fileSet, file, packageName)
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, stripRoutePrefix)
	if err != nil {
		return 0, fmt.Errorf("failed to analyze anonymous structs: %w", err)
	}

	if CLI.Verbose || len(result.AnonymousStructs) > 0 {
		log.Printf("Found %d anonymous structs in %s", len(result.AnonymousStructs), filePath)
	}

	if len(result.AnonymousStructs) == 0 {
		return 0, nil
	}

	// Show what we found
	printAnalysisResults(result)

	// Apply transformations if not dry run
	if !CLI.DryRun {
		// Use the DST transformer with the analysis results that include router information
		dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, stripRoutePrefix)
		modifiedSource, err := dstTransformer.Transform()
		if err != nil {
			return 0, fmt.Errorf("failed to transform file: %w", err)
		}

		// Write the modified source
		err = os.WriteFile(filePath, []byte(modifiedSource), 0644)
		if err != nil {
			return 0, fmt.Errorf("failed to write file: %w", err)
		}

		log.Printf("File transformed successfully: %s", filePath)
	} else {
		log.Printf("Dry run for %s: %d anonymous structs found", filePath, len(result.AnonymousStructs))
	}

	return len(result.AnonymousStructs), nil
}

func printAnalysisResults(result analyzer.FileAnalysisResult) {
	fmt.Printf("\n=== Analysis Results for %s ===\n", filepath.Base(result.FilePath))
	fmt.Printf("Package: %s\n", result.PackageName)
	fmt.Printf("Handlers found: %d\n", len(result.Handlers))
	fmt.Printf("Anonymous structs found: %d\n\n", len(result.AnonymousStructs))

	if len(result.AnonymousStructs) > 0 {
		fmt.Println("Anonymous structs:")
		for i, anonStruct := range result.AnonymousStructs {
			fmt.Printf("  %d. Variable '%s' in handler '%s' (%s binding)\n",
				i+1, anonStruct.VariableName, anonStruct.HandlerName, anonStruct.BindingType)
		}
		fmt.Println()
	}
}

package router

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// Route represents a single HTTP route with all its metadata (copied from gin-analyzer)
type Route struct {
	// HTTP method (GET, POST, PUT, DELETE, etc.)
	Method string

	// Full path including route group prefixes (e.g., "/api/v1/posts")
	Path string

	// Route group hierarchy (e.g., ["api", "v1"])
	RouteGroups []string

	// Handler name (e.g., "PostHandler" or "anonymous")
	Handler string

	// Package where the handler is defined
	HandlerPackage string
}

// RouteGroup represents a gin route group (copied from gin-analyzer)
type RouteGroup struct {
	// Variable name in the code (e.g., "api", "v1")
	Variable string

	// URL prefix (e.g., "/api", "/v1")
	Prefix string

	// Parent route group (for nested groups)
	Parent *RouteGroup

	// Full prefix path including parents (e.g., "/api/v1")
	FullPrefix string
}

// RouteInfo represents route information (moved from analyzer to avoid circular import)
type RouteInfo struct {
	// HTTP method (GET, POST, PUT, DELETE, etc.)
	Method string

	// Route path (/products/:id, /users, etc.)
	Path string

	// Whether this route information was inferred or explicit
	IsInferred bool

	// Route group hierarchy (e.g., ["api", "v1"] for /api/v1/users)
	RouteGroups []string
}

// HandlerInfo represents handler information interface
type HandlerInfo interface {
	GetName() string
	GetPackage() string
	GetRoute() *RouteInfo
	SetRoute(*RouteInfo)
}

// UpdateHandlerRouteInfo updates handler route information with actual router data
func (p *RouterParser) UpdateHandlerRouteInfo(handlers []HandlerInfo, routes []Route) error {
	// Create maps for faster lookup - support multiple routes per handler
	routeMap := make(map[string][]Route)
	for _, route := range routes {
		// Map full handler name (package.Handler) -> list of routes
		routeMap[route.Handler] = append(routeMap[route.Handler], route)
	}

	// Track which handlers got matched
	matchedHandlers := make(map[string]bool)
	var unmatchedHandlers []string

	// First pass: try to match exact handler names
	for _, handler := range handlers {
		fullHandlerName := handler.GetPackage() + "." + handler.GetName()
		if routes, exists := routeMap[fullHandlerName]; exists {
			// Use the first route found for this handler
			route := routes[0]
			handler.SetRoute(&RouteInfo{
				Method:      route.Method,
				Path:        route.Path,
				IsInferred:  false, // This is actual route data, not inferred
				RouteGroups: route.RouteGroups,
			})
			matchedHandlers[fullHandlerName] = true
		} else {
			// No exact match found - record this handler for second pass
			unmatchedHandlers = append(unmatchedHandlers, fullHandlerName)
		}
	}

	// Second pass: try to match inner handlers to their factory routes
	for _, handler := range handlers {
		handlerName := handler.GetName()
		if strings.HasSuffix(handlerName, "_inner") {
			// Extract the factory name by removing _inner suffix
			factoryName := strings.TrimSuffix(handlerName, "_inner")
			factoryFullName := handler.GetPackage() + "." + factoryName

			// Check if the factory handler has a route
			if routes, exists := routeMap[factoryFullName]; exists {
				// Copy the route info from the factory to the inner handler
				route := routes[0]
				handler.SetRoute(&RouteInfo{
					Method:      route.Method,
					Path:        route.Path,
					IsInferred:  false, // This is actual route data, not inferred
					RouteGroups: route.RouteGroups,
				})
				matchedHandlers[handler.GetPackage()+"."+handlerName] = true
			}
		}
	}

	// Collect final unmatched handlers
	var finalUnmatched []string
	for _, handlerName := range unmatchedHandlers {
		if !matchedHandlers[handlerName] {
			finalUnmatched = append(finalUnmatched, handlerName)
		}
	}

	if len(finalUnmatched) > 0 {
		return fmt.Errorf("no routes found for handlers: %v", finalUnmatched)
	}

	return nil
}

// RouterParser parses router files to extract route definitions (based on gin-analyzer)
type RouterParser struct {
	fileSet          *token.FileSet
	routerFilePath   string
	projectRoot      string
	routeGroups      map[string]*RouteGroup
	stripRoutePrefix string
}

// NewRouterParser creates a new router parser
func NewRouterParser(routerFilePath, stripRoutePrefix string) *RouterParser {
	return &RouterParser{
		fileSet:          token.NewFileSet(),
		routerFilePath:   routerFilePath,
		stripRoutePrefix: stripRoutePrefix,
		routeGroups:      make(map[string]*RouteGroup),
	}
}

// ParseRouterFile parses a router file and extracts route definitions
func (p *RouterParser) ParseRouterFile() ([]Route, error) {
	node, err := parser.ParseFile(p.fileSet, p.routerFilePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse router file: %w", err)
	}

	var routes []Route

	// Find router functions (functions that return *gin.Engine)
	ast.Inspect(node, func(n ast.Node) bool {
		if fn, ok := n.(*ast.FuncDecl); ok && p.isRouterFunction(fn) {
			routerRoutes := p.parseRouterFunction(fn, node.Name.Name)
			routes = append(routes, routerRoutes...)
		}
		return true
	})

	return routes, nil
}

// isRouterFunction checks if a function returns *gin.Engine
func (p *RouterParser) isRouterFunction(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}

	result := fn.Type.Results.List[0]
	if starExpr, ok := result.Type.(*ast.StarExpr); ok {
		if selExpr, ok := starExpr.X.(*ast.SelectorExpr); ok {
			if x, ok := selExpr.X.(*ast.Ident); ok {
				return x.Name == "gin" && selExpr.Sel.Name == "Engine"
			}
		}
	}
	return false
}

// parseRouterFunction parses a router function and extracts all routes
func (p *RouterParser) parseRouterFunction(fn *ast.FuncDecl, packageName string) []Route {
	var routes []Route

	// Find the main router variable (usually r := gin.Default())
	var routerVar *ast.Ident
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if routerVar != nil {
			return false
		}
		if assignStmt, ok := n.(*ast.AssignStmt); ok {
			if len(assignStmt.Lhs) == 1 && len(assignStmt.Rhs) == 1 {
				if call, ok := assignStmt.Rhs[0].(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "gin" {
							if sel.Sel.Name == "Default" || sel.Sel.Name == "New" {
								if ident, ok := assignStmt.Lhs[0].(*ast.Ident); ok {
									routerVar = ident
									return false
								}
							}
						}
					}
				}
			}
		}
		return true
	})

	if routerVar == nil {
		return routes
	}

	// Initialize route groups with the main router
	p.routeGroups = make(map[string]*RouteGroup)
	p.routeGroups[routerVar.Name] = &RouteGroup{
		Variable:   routerVar.Name,
		Prefix:     "",
		Parent:     nil,
		FullPrefix: "",
	}

	// Parse route groups and individual routes
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			// Handle route group assignments: api := r.Group("/api")
			p.parseRouteGroupAssignment(node)
		case *ast.CallExpr:
			// Handle HTTP method calls: r.GET("/path", handler)
			if route := p.parseRouteCall(node, packageName); route != nil {
				routes = append(routes, *route)
			}
		}
		return true
	})

	return routes
}

// parseRouteGroupAssignment parses route group assignments
func (p *RouterParser) parseRouteGroupAssignment(node *ast.AssignStmt) {
	if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
		return
	}

	ident, ok := node.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}

	call, ok := node.Rhs[0].(*ast.CallExpr)
	if !ok {
		return
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Group" || len(call.Args) == 0 {
		return
	}

	parentIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}

	parent, exists := p.routeGroups[parentIdent.Name]
	if !exists {
		return
	}

	pathLit, ok := call.Args[0].(*ast.BasicLit)
	if !ok {
		return
	}

	groupPrefix := strings.Trim(pathLit.Value, `"`)
	fullPrefix := parent.FullPrefix + groupPrefix

	p.routeGroups[ident.Name] = &RouteGroup{
		Variable:   ident.Name,
		Prefix:     groupPrefix,
		Parent:     parent,
		FullPrefix: fullPrefix,
	}
}

// parseRouteCall parses HTTP method calls and creates Route objects
func (p *RouterParser) parseRouteCall(node *ast.CallExpr, packageName string) *Route {
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	// Check if it's an HTTP method
	httpMethods := map[string]bool{
		"GET": true, "POST": true, "PUT": true, "DELETE": true,
		"PATCH": true, "HEAD": true, "OPTIONS": true,
	}

	if !httpMethods[sel.Sel.Name] || len(node.Args) < 2 {
		return nil
	}

	// Get the router/group variable
	groupIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}

	group, exists := p.routeGroups[groupIdent.Name]
	if !exists {
		return nil
	}

	// Get the path
	pathLit, ok := node.Args[0].(*ast.BasicLit)
	if !ok {
		return nil
	}

	relativePath := strings.Trim(pathLit.Value, `"`)
	fullPath := group.FullPrefix + relativePath

	// Get the handler
	handlerArg := node.Args[len(node.Args)-1]
	handler, handlerPackage := p.extractHandler(handlerArg, packageName)

	// Build route groups hierarchy
	var routeGroupsHierarchy []string
	current := group
	for current != nil && current.Prefix != "" {
		groupName := strings.Trim(current.Prefix, "/")
		// Apply strip prefix if configured
		if p.stripRoutePrefix != "" {
			groupName = strings.TrimPrefix(groupName, strings.Trim(p.stripRoutePrefix, "/"))
			// Remove any leading/trailing slashes after stripping
			groupName = strings.Trim(groupName, "/")
			// Only add if not empty after stripping
			if groupName != "" {
				routeGroupsHierarchy = append([]string{groupName}, routeGroupsHierarchy...)
			}
		} else {
			routeGroupsHierarchy = append([]string{groupName}, routeGroupsHierarchy...)
		}
		current = current.Parent
	}

	return &Route{
		Method:         sel.Sel.Name,
		Path:           fullPath,
		RouteGroups:    routeGroupsHierarchy,
		Handler:        handler,
		HandlerPackage: handlerPackage,
	}
}

// extractHandler extracts handler information from a handler argument
func (p *RouterParser) extractHandler(handlerArg ast.Expr, packageName string) (string, string) {
	switch h := handlerArg.(type) {
	case *ast.Ident:
		// Named function: Handler
		return h.Name, packageName
	case *ast.SelectorExpr:
		// External handler: package.Handler
		if x, ok := h.X.(*ast.Ident); ok {
			return x.Name + "." + h.Sel.Name, x.Name
		}
	case *ast.FuncLit:
		// Anonymous function
		return "anonymous", packageName
	case *ast.CallExpr:
		// Function call (handler factory): HandlerFactory()
		if fun, ok := h.Fun.(*ast.Ident); ok {
			// Return the function name being called (factory function)
			return fun.Name, packageName
		}
		// Handle selector expressions in function calls (e.g., pkg.HandlerFactory())
		if sel, ok := h.Fun.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				return x.Name + "." + sel.Sel.Name, x.Name
			}
		}
	}
	return "unknown", packageName
}


// ParseRouterAndAnalyzeHandlers parses a router file and updates handler analysis
func ParseRouterAndAnalyzeHandlers(routerFilePath string, handlers []HandlerInfo, stripRoutePrefix string) error {
	parser := NewRouterParser(routerFilePath, stripRoutePrefix)

	routes, err := parser.ParseRouterFile()
	if err != nil {
		return fmt.Errorf("failed to parse router file %s: %w", routerFilePath, err)
	}

	if len(routes) == 0 {
		return fmt.Errorf("no routes found in router file %s", routerFilePath)
	}

	// Update handlers with actual route information
	if err := parser.UpdateHandlerRouteInfo(handlers, routes); err != nil {
		return fmt.Errorf("failed to match handlers to routes: %w", err)
	}

	return nil
}
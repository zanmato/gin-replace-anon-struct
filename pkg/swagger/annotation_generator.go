package swagger

import (
	"fmt"
	"go/ast"
	"go/token"
	"net/http"
	"regexp"
	"strings"

	"github.com/andreas/gin-replace-anon-struct/pkg/analyzer"
	"github.com/andreas/gin-replace-anon-struct/pkg/namer"
	"github.com/andreas/gin-replace-anon-struct/pkg/router"
)

var pathParamRegex = regexp.MustCompile(`:([a-z]+)`)

// AnnotationGenerator generates Swagger annotations for handlers
type AnnotationGenerator struct {
	analysis         analyzer.FileAnalysisResult
	stripRoutePrefix string
}

// NewAnnotationGenerator creates a new annotation generator
func NewAnnotationGenerator(analysis analyzer.FileAnalysisResult, stripRoutePrefix string) *AnnotationGenerator {
	return &AnnotationGenerator{
		analysis:         analysis,
		stripRoutePrefix: stripRoutePrefix,
	}
}

// GenerateAnnotations generates Swagger annotations for all handlers
func (g *AnnotationGenerator) GenerateAnnotations() []HandlerAnnotations {
	var annotations []HandlerAnnotations

	for _, handler := range g.analysis.Handlers {
		handlerAnnotations := g.generateHandlerAnnotations(handler)
		if len(handlerAnnotations.Annotations) > 0 {
			annotations = append(annotations, handlerAnnotations)
		}
	}

	return annotations
}

// HandlerAnnotations contains Swagger annotations for a single handler
type HandlerAnnotations struct {
	// Handler function name
	HandlerName string

	// Existing documentation comment
	ExistingComment string

	// Swagger annotations to add
	Annotations []string

	// AST function declaration for position information
	FuncDecl *ast.FuncDecl
}

// generateHandlerAnnotations generates annotations for a single handler
func (g *AnnotationGenerator) generateHandlerAnnotations(handler analyzer.HandlerInfo) HandlerAnnotations {
	var annotations []string

	// Generate @id annotation (should come first)
	idAnnotation := g.generateIDAnnotation(handler)
	if idAnnotation != "" {
		annotations = append(annotations, idAnnotation)
	}

	// Generate @Tags annotations for router groups (should come after @id)
	if handler.Route != nil && len(handler.Route.RouteGroups) > 0 {
		tagsAnnotation := g.generateTagsAnnotation(*handler.Route)
		if tagsAnnotation != "" {
			annotations = append(annotations, tagsAnnotation)
		}
	}

	// Generate @Param annotations for path parameters (should come after @Tags)
	if handler.Route != nil {
		pathParamAnnotations := g.generatePathParamAnnotations(*handler.Route)
		annotations = append(annotations, pathParamAnnotations...)
	}

	// Generate @Param annotations for parameter structs (query, JSON body, etc.)
	// These should come before @Success annotations as requested
	for _, anonStruct := range handler.AnonymousStructs {
		if !anonStruct.IsResponse && anonStruct.BindingCall != nil {
			// Generate @Param annotation only for request parameter structs that are actually bound
			paramAnnotation := g.generateParamAnnotation(anonStruct, handler)
			if paramAnnotation != nil {
				annotations = append(annotations, paramAnnotation...)
			}
		}
	}

	// Generate @Param annotations for named type bindings
	for _, namedBinding := range handler.NamedTypeBindings {
		paramAnnotation := g.generateNamedParamAnnotation(namedBinding, handler)
		if paramAnnotation != nil {
			annotations = append(annotations, paramAnnotation...)
		}
	}

	// Find response structs for this handler
	for _, anonStruct := range handler.AnonymousStructs {
		if anonStruct.IsResponse {
			// Generate @Success annotation
			successAnnotation := g.generateSuccessAnnotation(anonStruct, handler)
			if successAnnotation != nil {
				annotations = append(annotations, successAnnotation...)
			}
		}
	}

	// Generate @Success annotations for named type responses
	for _, namedResponse := range handler.NamedTypeResponses {
		successAnnotation := g.generateNamedSuccessAnnotation(namedResponse, handler)
		if successAnnotation != nil {
			annotations = append(annotations, successAnnotation...)
		}
	}

	// Check if we have any success annotations
	hasSuccessAnnotation := false
	for _, annotation := range annotations {
		if strings.HasPrefix(annotation, "@Success") {
			hasSuccessAnnotation = true
			break
		}
	}

	// If no success annotations found, try to extract from c.Status() calls as fallback
	if !hasSuccessAnnotation {
		statusCode := g.extractStatusCodeFromHandler(handler)
		if statusCode > 0 {
			// Generate a basic success annotation with the extracted status code
			annotations = append(annotations, "@Produce plain", fmt.Sprintf("@Success %d {string} string", statusCode))
		}
	}

	// Generate @Router annotation
	if handler.Route != nil {
		routerAnnotation := g.generateRouterAnnotation(*handler.Route)
		if routerAnnotation != "" {
			annotations = append(annotations, routerAnnotation)
		}
	}

	return HandlerAnnotations{
		HandlerName:     handler.Name,
		ExistingComment: handler.DocComment,
		Annotations:     annotations,
		FuncDecl:        handler.FuncDecl,
	}
}

// extractStatusCodeFromHandler extracts HTTP status codes from c.Status() calls in a handler
func (g *AnnotationGenerator) extractStatusCodeFromHandler(handler analyzer.HandlerInfo) int {
	if handler.FuncDecl == nil || handler.FuncDecl.Body == nil {
		return 0
	}

	// Walk through the function body to find c.Status() calls
	var statusCode int
	ast.Inspect(handler.FuncDecl.Body, func(n ast.Node) bool {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			// Check if this is a c.Status() call
			if g.isStatusCall(callExpr) {
				if extractedCode := g.extractStatusCodeFromCall(callExpr); extractedCode > 0 {
					statusCode = extractedCode
					return false // Stop after finding the first one
				}
			}
		}
		return true
	})

	return statusCode
}

// isStatusCall checks if a call expression is c.Status(...)
func (g *AnnotationGenerator) isStatusCall(callExpr *ast.CallExpr) bool {
	if len(callExpr.Args) < 1 {
		return false
	}

	// Check if the function being called is .Status on a gin.Context
	selExpr, ok := callExpr.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	// Check if the selector is "Status"
	if selExpr.Sel.Name != "Status" {
		return false
	}

	// Check if the receiver is a variable (likely "c")
	_, ok = selExpr.X.(*ast.Ident)
	return ok
}

// extractStatusCodeFromCall extracts the status code from a c.Status() call
func (g *AnnotationGenerator) extractStatusCodeFromCall(callExpr *ast.CallExpr) int {
	if len(callExpr.Args) < 1 {
		return 0
	}

	// Get the first argument (the status code)
	arg := callExpr.Args[0]

	// Handle different types of expressions
	switch expr := arg.(type) {
	case *ast.Ident:
		// Handle identifiers like "http.StatusOK"
		return g.mapIdentifierToStatusCode(expr.Name)
	case *ast.SelectorExpr:
		// Handle expressions like "http.StatusOK" or "http.StatusCreated"
		if g.isHTTPStatusSelector(expr) {
			return g.mapSelectorToStatusCode(expr)
		}
	case *ast.BasicLit:
		// Handle numeric literals like "200" or "201"
		if expr.Kind == token.INT {
			return g.parseIntLiteral(expr.Value)
		}
	}

	return 0
}

// isHTTPStatusSelector checks if a selector expression is an HTTP status constant
func (g *AnnotationGenerator) isHTTPStatusSelector(selExpr *ast.SelectorExpr) bool {
	// Check if the package is "http"
	if ident, ok := selExpr.X.(*ast.Ident); ok && ident.Name == "http" {
		// Check if the selector is a known status method
		switch selExpr.Sel.Name {
		case "StatusOK", "StatusCreated", "StatusAccepted", "StatusNoContent",
			"StatusBadRequest", "StatusUnauthorized", "StatusForbidden",
			"StatusNotFound", "StatusMethodNotAllowed", "StatusConflict",
			"StatusUnprocessableEntity", "StatusInternalServerError",
			"StatusBadGateway", "StatusServiceUnavailable", "StatusGatewayTimeout":
			return true
		}
	}
	return false
}

// mapSelectorToStatusCode maps HTTP status selectors to their numeric values
func (g *AnnotationGenerator) mapSelectorToStatusCode(selExpr *ast.SelectorExpr) int {
	switch selExpr.Sel.Name {
	case "StatusOK":
		return http.StatusOK
	case "StatusCreated":
		return http.StatusCreated
	case "StatusAccepted":
		return http.StatusAccepted
	case "StatusNoContent":
		return http.StatusNoContent
	case "StatusBadRequest":
		return http.StatusBadRequest
	case "StatusUnauthorized":
		return http.StatusUnauthorized
	case "StatusForbidden":
		return http.StatusForbidden
	case "StatusNotFound":
		return http.StatusNotFound
	case "StatusMethodNotAllowed":
		return http.StatusMethodNotAllowed
	case "StatusConflict":
		return http.StatusConflict
	case "StatusUnprocessableEntity":
		return http.StatusUnprocessableEntity
	case "StatusInternalServerError":
		return http.StatusInternalServerError
	case "StatusBadGateway":
		return http.StatusBadGateway
	case "StatusServiceUnavailable":
		return http.StatusServiceUnavailable
	case "StatusGatewayTimeout":
		return http.StatusGatewayTimeout
	default:
		return 0
	}
}

// mapIdentifierToStatusCode maps simple identifiers to status codes
func (g *AnnotationGenerator) mapIdentifierToStatusCode(name string) int {
	switch name {
	case "StatusOK", "ok":
		return http.StatusOK
	case "StatusCreated", "created":
		return http.StatusCreated
	case "StatusAccepted", "accepted":
		return http.StatusAccepted
	case "StatusNoContent", "noContent":
		return http.StatusNoContent
	default:
		return 0
	}
}

// parseIntLiteral parses an integer literal string to an int
func (g *AnnotationGenerator) parseIntLiteral(value string) int {
	var code int
	fmt.Sscanf(value, "%d", &code)
	return code
}

// generateIDAnnotation generates @id annotation for a handler
func (g *AnnotationGenerator) generateIDAnnotation(handler analyzer.HandlerInfo) string {
	if handler.Name == "" {
		return ""
	}

	// Extract package name and handler name
	packageName := handler.PackageName
	handlerName := handler.Name

	// If handler name includes package name (e.g., "common.Currencies"), split it
	if dotIndex := strings.LastIndex(handlerName, "."); dotIndex != -1 {
		packageName = handlerName[:dotIndex]
		handlerName = handlerName[dotIndex+1:]
	}

	// Generate ID: uppercase first letter of package name + handler name
	if packageName != "" && handlerName != "" {
		// Uppercase first letter of package name
		if len(packageName) > 0 {
			packageName = strings.ToUpper(packageName[:1]) + packageName[1:]
		}
		return fmt.Sprintf("@id %s%s", packageName, handlerName)
	}

	// Fallback: just use the handler name if no package info
	return fmt.Sprintf("@id %s", handlerName)
}

// generateSuccessAnnotation generates @Success annotation for a response struct
func (g *AnnotationGenerator) generateSuccessAnnotation(anonStruct analyzer.AnonymousStructInfo, handler analyzer.HandlerInfo) []string {
	// This will be filled by the transformer after type names are generated
	typeName := anonStruct.GeneratedTypeName
	if typeName == "" {
		typeName = g.inferTypeName(anonStruct, handler)
	}

	statusCode := anonStruct.HTTPStatusCode
	if statusCode == 0 {
		statusCode = 200 // Default
	}

	// Determine if we should use {array} or {object}
	schemaType := "{object}"
	if anonStruct.IsArray {
		schemaType = "{array}"
	}

	return []string{"@Produce json", fmt.Sprintf("@Success %d %s %s", statusCode, schemaType, typeName)}
}

// generateParamAnnotation generates @Param annotation for request parameter structs
func (g *AnnotationGenerator) generateParamAnnotation(anonStruct analyzer.AnonymousStructInfo, handler analyzer.HandlerInfo) []string {
	// This will be filled by the transformer after type names are generated
	typeName := anonStruct.GeneratedTypeName
	if typeName == "" {
		typeName = g.inferTypeName(anonStruct, handler)
	}

	// Generate @Param annotation based on binding type
	switch anonStruct.BindingType {
	case analyzer.BindingTypeQuery:
		// Format: @Param data query STRUCT_NAME false
		return []string{fmt.Sprintf("@Param request query %s false \"Query parameters\"", typeName)}
	case analyzer.BindingTypeJSON:
		// Format: @Param json body STRUCT_NAME true
		return []string{
			"@Accept json", fmt.Sprintf("@Param request body %s true \"Request body\"", typeName)}
	case analyzer.BindingTypeForm:
		// Format: @Param formData form STRUCT_NAME true
		return []string{
			"@Accept mpfd", fmt.Sprintf("@Param request form %s true \"Form data\"", typeName)}
	default:
		// Default to query for unknown types
		return []string{fmt.Sprintf("@Param request query %s false \"Query parameters\"", typeName)}
	}
}

// generateNamedParamAnnotation generates @Param annotation for named type bindings
func (g *AnnotationGenerator) generateNamedParamAnnotation(binding analyzer.NamedTypeBindingInfo, _ analyzer.HandlerInfo) []string {
	// Generate @Param annotation based on binding type
	switch binding.BindingType {
	case analyzer.BindingTypeQuery:
		// Format: @Param data query TYPE_NAME false
		return []string{fmt.Sprintf("@Param request query %s false \"Query parameters\"", binding.TypeName)}
	case analyzer.BindingTypeJSON:
		// Format: @Param json body TYPE_NAME true
		return []string{
			"@Accept json",
			fmt.Sprintf("@Param request body %s true \"Request body\"", binding.TypeName),
		}
	case analyzer.BindingTypeForm:
		// Format: @Param formData form TYPE_NAME true
		return []string{
			"@Accept mpfd",
			fmt.Sprintf("@Param request body %s true \"Form data\"", binding.TypeName),
		}
	default:
		// Default to query for unknown types
		return []string{fmt.Sprintf("@Param request query %s false \"Query parameters\"", binding.TypeName)}
	}
}

// generateNamedSuccessAnnotation generates @Success annotation for named type responses
func (g *AnnotationGenerator) generateNamedSuccessAnnotation(response analyzer.NamedTypeResponseInfo, _ analyzer.HandlerInfo) []string {
	statusCode := response.HTTPStatusCode
	if statusCode == 0 {
		statusCode = 200 // Default
	}

	// Determine if we should use {array} or {object}
	schemaType := "{object}"
	typeName := response.TypeName
	if response.IsArray {
		schemaType = "{array}"
		// Remove the [] prefix from type name when it's an array
		typeName = strings.TrimPrefix(typeName, "[]")
	}

	return []string{"@Produce json", fmt.Sprintf("@Success %d %s %s", statusCode, schemaType, typeName)}
}

// generateTagsAnnotation generates @Tags annotation for router groups
func (g *AnnotationGenerator) generateTagsAnnotation(route router.RouteInfo) string {
	if len(route.RouteGroups) == 0 {
		return ""
	}

	// Start with the full path and apply strip prefix if configured
	path := route.Path
	if g.stripRoutePrefix != "" {
		path = strings.TrimPrefix(path, g.stripRoutePrefix)
		// Remove any leading slashes after stripping
		path = strings.TrimLeft(path, "/")
		// Add leading slash back for consistent parsing
		if path != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}

	// Extract all path segments before the first parameter
	// For example:
	// /shop/orders/:id/checks => ["shop", "orders"]
	// /shop/orders => ["shop", "orders"]
	// /shop/orders/:id => ["shop", "orders"]
	pathSegments := extractPathSegmentsBeforeFirstParam(path)

	// Take the first two segments as tags (or fewer if less exist)
	var tagSegments []string
	for i := 0; i < len(pathSegments) && i < 2; i++ {
		segment := strings.TrimSpace(pathSegments[i])
		if segment != "" {
			// Handle hyphens by splitting and capitalizing each part
			// e.g., "customer-accounts" -> "CustomerAccounts"
			parts := strings.Split(segment, "-")
			var pascalParts []string
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part != "" {
					// Capitalize first letter of each part
					if len(part) > 0 {
						part = strings.ToUpper(part[:1]) + part[1:]
					}
					pascalParts = append(pascalParts, part)
				}
			}
			// Join the parts without separators for PascalCase
			pascalSegment := strings.Join(pascalParts, "")
			tagSegments = append(tagSegments, pascalSegment)
		}
	}

	if len(tagSegments) == 0 {
		return ""
	}

	// Join without separators for PascalCase format
	tag := strings.Join(tagSegments, "")

	return fmt.Sprintf("@Tags %s", tag)
}

// extractPathSegmentsBeforeFirstParam extracts all path segments before the first parameter
// For example:
// /shop/orders/:id/checks => ["shop", "orders"]
// /shop/orders => ["shop", "orders"]
// /shop/orders/:id => ["shop", "orders"]
// /templates => ["templates"]
// /templates/:id => ["templates"]
// /suppliers/:id/accounts => ["suppliers"]
// /cross-selling/:id/products/:bid => ["cross-selling"]
func extractPathSegmentsBeforeFirstParam(path string) []string {
	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	// Split by slash
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return []string{}
	}

	// Find first part that starts with ':' (parameter marker)
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			// Return all parts before the first parameter
			if i > 0 {
				return parts[:i]
			}
			// If the first part is a parameter, return empty
			return []string{}
		}
	}

	// No parameters found, return all parts
	return parts
}

// generateRouterAnnotation generates @Router annotation
func (g *AnnotationGenerator) generateRouterAnnotation(route router.RouteInfo) string {
	method := strings.ToUpper(route.Method)

	// Replace :paramName with {paramName}
	path := pathParamRegex.ReplaceAllString(route.Path, "{$1}")

	return fmt.Sprintf("@Router %s [%s]", path, method)
}

// generatePathParamAnnotations generates @Param annotations for path parameters
func (g *AnnotationGenerator) generatePathParamAnnotations(route router.RouteInfo) []string {
	var annotations []string

	// Extract path parameters from the route path
	pathParams := g.extractPathParameters(route.Path)

	for _, param := range pathParams {
		paramType := g.inferParameterType(param.Name)
		description := g.generateParameterDescription(param.Name, route.Path)

		var suffix string
		if paramType == "uuid" {
			paramType = "string"
			suffix = " format(uuid)"
		}

		// Format: @Param paramName path paramType true "Description"
		annotation := fmt.Sprintf("@Param %s path %s true \"%s\"%s", param.Name, paramType, description, suffix)

		annotations = append(annotations, annotation)
	}

	return annotations
}

// PathParameter represents a path parameter extracted from a route
type PathParameter struct {
	Name string
	// Position in the path (for future use)
	Position int
}

// extractPathParameters extracts all path parameters from a route path
// For example: "/api/product/attributes/:id/options/:oid" -> [{Name: "id"}, {Name: "oid"}]
func (g *AnnotationGenerator) extractPathParameters(path string) []PathParameter {
	var params []PathParameter

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	// Split by slash
	parts := strings.Split(path, "/")

	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			// Remove the ':' prefix to get the parameter name
			paramName := strings.TrimPrefix(part, ":")
			params = append(params, PathParameter{
				Name:     paramName,
				Position: i,
			})
		}
	}

	return params
}

// inferParameterType determines the Swagger parameter type based on parameter name
func (g *AnnotationGenerator) inferParameterType(paramName string) string {
	// Common patterns for UUID parameters
	uuidPatterns := []string{"id", "uid", "uuid"}
	paramNameLower := strings.ToLower(paramName)

	for _, pattern := range uuidPatterns {
		if strings.Contains(paramNameLower, pattern) {
			return "uuid"
		}
	}

	// Default to string for other parameters
	return "string"
}

// generateParameterDescription generates a human-readable description for a path parameter
func (g *AnnotationGenerator) generateParameterDescription(paramName, fullPath string) string {
	// Extract the context from the path to create a better description
	// For example: "/api/product/attributes/:id/options/:oid"
	// For param "id" -> "Attributes ID"
	// For param "oid" -> "Options ID"

	// Remove leading slash
	path := strings.TrimPrefix(fullPath, "/")

	// Split by slash
	parts := strings.Split(path, "/")

	// Find the parameter position and get the preceding segment as context
	for i, part := range parts {
		if strings.HasPrefix(part, ":") && strings.TrimPrefix(part, ":") == paramName {
			if i > 0 {
				// Get the preceding segment as context
				context := parts[i-1]

				// Convert context to singular form and capitalize
				singularContext := g.singularize(context)
				capitalizedContext := strings.Title(singularContext)

				// Capitalize the parameter name
				capitalizedParam := strings.Title(paramName)

				return fmt.Sprintf("%s %s", capitalizedContext, capitalizedParam)
			}
			break
		}
	}

	// Fallback: just capitalize the parameter name
	return strings.Title(paramName)
}

// singularize attempts to convert a plural word to singular form
// This is a simple implementation for common cases
func (g *AnnotationGenerator) singularize(word string) string {
	word = strings.ToLower(word)

	// Common plural to singular mappings
	singularizations := map[string]string{
		"attributes":  "attribute",
		"options":     "option",
		"products":    "product",
		"orders":      "order",
		"users":       "user",
		"accounts":    "account",
		"templates":   "template",
		"suppliers":   "supplier",
		"categories":  "category",
		"items":       "item",
		"values":      "value",
		"statuses":    "status",
		"types":       "type",
		"roles":       "role",
		"permissions": "permission",
	}

	if singular, exists := singularizations[word]; exists {
		return singular
	}

	// Simple rules for common patterns
	if strings.HasSuffix(word, "ies") {
		return strings.TrimSuffix(word, "ies") + "y"
	}
	if strings.HasSuffix(word, "es") {
		return strings.TrimSuffix(word, "es")
	}
	if strings.HasSuffix(word, "s") && len(word) > 1 {
		return strings.TrimSuffix(word, "s")
	}

	return word
}

// inferTypeName infers type name when GeneratedTypeName is not yet set
func (g *AnnotationGenerator) inferTypeName(anonStruct analyzer.AnonymousStructInfo, handler analyzer.HandlerInfo) string {
	// Use the same logic as the namer to generate consistent type names
	generator := namer.NewTypeNameGenerator()

	// Clean up handler name (remove package prefix if present)
	handlerName := generator.CleanHandlerName(handler.Name)

	// Generate base name based on binding type and context
	baseName := generator.GenerateBaseName(handlerName, anonStruct.VariableName, anonStruct.BindingType)

	// Make sure it's a valid Go type name (first letter uppercase)
	typeName := generator.MakeValidTypeName(baseName)

	return typeName
}

// FormatAnnotations formats and inserts Swagger annotations into the source code
func (g *AnnotationGenerator) FormatAnnotations(source string, annotations []HandlerAnnotations) string {
	if len(annotations) == 0 {
		return source
	}

	// Sort annotations by position (from bottom to top to avoid position shifts)
	sortedAnnotations := make([]HandlerAnnotations, len(annotations))
	copy(sortedAnnotations, annotations)

	// We'll apply from top to bottom since we're inserting, which shifts positions
	// For simplicity, let's just do a basic approach here

	return source // Placeholder - actual implementation will be in transformer
}

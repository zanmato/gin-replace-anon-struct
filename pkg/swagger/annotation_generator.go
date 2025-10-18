package swagger

import (
	"fmt"
	"go/ast"
	"strings"

	"gin-replace-anon-struct/pkg/analyzer"
	"gin-replace-anon-struct/pkg/router"
)

// AnnotationGenerator generates Swagger annotations for handlers
type AnnotationGenerator struct {
	analysis analyzer.FileAnalysisResult
}

// NewAnnotationGenerator creates a new annotation generator
func NewAnnotationGenerator(analysis analyzer.FileAnalysisResult) *AnnotationGenerator {
	return &AnnotationGenerator{
		analysis: analysis,
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

	// Generate @Tags annotations for router groups (should come first)
	if handler.Route != nil && len(handler.Route.RouteGroups) > 0 {
		tagsAnnotation := g.generateTagsAnnotation(*handler.Route)
		if tagsAnnotation != "" {
			annotations = append(annotations, tagsAnnotation)
		}
	}

	// Generate @Param annotations for parameter structs (query, JSON body, etc.)
	// These should come before @Success annotations as requested
	for _, anonStruct := range handler.AnonymousStructs {
		if !anonStruct.IsResponse {
			// Generate @Param annotation for request parameter structs
			paramAnnotation := g.generateParamAnnotation(anonStruct, handler)
			if paramAnnotation != "" {
				annotations = append(annotations, paramAnnotation)
			}
		}
	}

	// Find response structs for this handler
	for _, anonStruct := range handler.AnonymousStructs {
		if anonStruct.IsResponse {
			// Generate @Success annotation
			successAnnotation := g.generateSuccessAnnotation(anonStruct, handler)
			if successAnnotation != "" {
				annotations = append(annotations, successAnnotation)
			}
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

// generateSuccessAnnotation generates @Success annotation for a response struct
func (g *AnnotationGenerator) generateSuccessAnnotation(anonStruct analyzer.AnonymousStructInfo, handler analyzer.HandlerInfo) string {
	// This will be filled by the transformer after type names are generated
	typeName := anonStruct.GeneratedTypeName
	if typeName == "" {
		typeName = g.inferTypeName(anonStruct, handler)
	}

	statusCode := anonStruct.HTTPStatusCode
	if statusCode == 0 {
		statusCode = 200 // Default
	}

	return fmt.Sprintf("@Success %d {object} %s", statusCode, typeName)
}

// generateParamAnnotation generates @Param annotation for request parameter structs
func (g *AnnotationGenerator) generateParamAnnotation(anonStruct analyzer.AnonymousStructInfo, handler analyzer.HandlerInfo) string {
	// This will be filled by the transformer after type names are generated
	typeName := anonStruct.GeneratedTypeName
	if typeName == "" {
		typeName = g.inferTypeName(anonStruct, handler)
	}

	// Generate @Param annotation based on binding type
	switch anonStruct.BindingType {
	case analyzer.BindingTypeQuery:
		// Format: @Param data query STRUCT_NAME false
		return fmt.Sprintf("@Param data query %s false", typeName)
	case analyzer.BindingTypeJSON:
		// Format: @Param json body STRUCT_NAME true
		return fmt.Sprintf("@Param json body %s true", typeName)
	case analyzer.BindingTypeForm:
		// Format: @Param formData form STRUCT_NAME true
		return fmt.Sprintf("@Param formData form %s true", typeName)
	default:
		// Default to query for unknown types
		return fmt.Sprintf("@Param data query %s false", typeName)
	}
}

// generateTagsAnnotation generates @Tags annotation for router groups
func (g *AnnotationGenerator) generateTagsAnnotation(route router.RouteInfo) string {
	if len(route.RouteGroups) == 0 {
		return ""
	}

	// Create tag from route groups hierarchy
	// For example, ["api", "v1"] becomes "api/v1"
	tag := strings.Join(route.RouteGroups, "/")

	return fmt.Sprintf("@Tags %s", tag)
}

// generateRouterAnnotation generates @Router annotation
func (g *AnnotationGenerator) generateRouterAnnotation(route router.RouteInfo) string {
	method := strings.ToUpper(route.Method)
	path := route.Path

	return fmt.Sprintf("@Router %s [%s]", path, method)
}

// inferTypeName infers type name when GeneratedTypeName is not yet set
func (g *AnnotationGenerator) inferTypeName(anonStruct analyzer.AnonymousStructInfo, handler analyzer.HandlerInfo) string {
	handlerName := strings.ToLower(handler.Name)

	switch anonStruct.BindingType {
	case analyzer.BindingTypeResponse:
		return handlerName + "Response"
	case analyzer.BindingTypeJSON:
		return handlerName + "Request"
	case analyzer.BindingTypeQuery:
		return handlerName + "QueryParams"
	case analyzer.BindingTypeForm:
		return handlerName + "FormData"
	default:
		return handlerName + "Data"
	}
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

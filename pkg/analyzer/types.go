package analyzer

import (
	"go/ast"
	"go/token"

	"github.com/andreas/gin-replace-anon-struct/pkg/router"
)

// BindingType represents the type of binding being used
type BindingType string

const (
	BindingTypeJSON    BindingType = "json"    // ShouldBindJSON, BindJSON
	BindingTypeQuery   BindingType = "query"   // ShouldBindQuery, BindQuery
	BindingTypeForm    BindingType = "form"    // ShouldBind, Bind (with form tags)
	BindingTypeBind    BindingType = "bind"    // ShouldBind, Bind (generic)
	BindingTypeResponse BindingType = "response" // c.JSON response structs
)

// AnonymousStructInfo represents information about an anonymous struct found in a handler
type AnonymousStructInfo struct {
	// Variable name (e.g., "q", "req", "body")
	VariableName string

	// Handler function name where this struct was found
	HandlerName string

	// Type of binding (json, query, etc.)
	BindingType BindingType

	// The AST struct type definition
	StructType *ast.StructType

	// Position in the source file
	Position token.Pos

	// The complete assignment statement containing the anonymous struct
	AssignmentStmt *ast.AssignStmt

	// For var declarations (var req struct{})
	ValueSpec *ast.ValueSpec

	// The binding call that uses this variable (c.ShouldBindJSON(&req))
	BindingCall *ast.CallExpr

	// For response structs: the c.JSON call containing this struct
	ResponseCall *ast.CallExpr

	// Whether this struct is used in a response (c.JSON call)
	IsResponse bool

	// Whether this struct is an array/slice (e.g., []struct{})
	IsArray bool

	// HTTP status code used in the response (for response structs)
	HTTPStatusCode int

	// Generated type name (e.g., "ProductsRequest", "ProductsQueryParams")
	GeneratedTypeName string
}

// NamedTypeBindingInfo represents information about named type bindings in handlers
type NamedTypeBindingInfo struct {
	// Variable name (e.g., "a", "req", "body")
	VariableName string

	// Type name (e.g., "AccountAddress", "Product")
	TypeName string

	// Type of binding (json, query, etc.)
	BindingType BindingType

	// The binding call that uses this variable (c.ShouldBindJSON(&a))
	BindingCall *ast.CallExpr

	// Position in the source file
	Position token.Pos
}

// NamedTypeResponseInfo represents information about named type responses in handlers
type NamedTypeResponseInfo struct {
	// Variable name (e.g., "products", "p", "response")
	VariableName string

	// Type name (e.g., "Product", "Account", "Category")
	TypeName string

	// Whether this is an array/slice (e.g., []Product)
	IsArray bool

	// HTTP status code used in the response
	HTTPStatusCode int

	// The c.JSON call containing this type
	ResponseCall *ast.CallExpr

	// Position in the source file
	Position token.Pos
}

// HandlerInfo represents information about a Gin handler function
type HandlerInfo struct {
	// Function name
	Name string

	// AST function declaration
	FuncDecl *ast.FuncDecl

	// Whether this is a handler factory (returns gin.HandlerFunc)
	IsFactory bool

	// Anonymous structs found in this handler
	AnonymousStructs []AnonymousStructInfo

	// Named type bindings found in this handler (for @Param annotations)
	NamedTypeBindings []NamedTypeBindingInfo

	// Named type responses found in this handler (for @Success annotations)
	NamedTypeResponses []NamedTypeResponseInfo

	// Package name
	PackageName string

	// Route information for Swagger generation
	Route *router.RouteInfo

	// Existing comment documentation before the function
	DocComment string
}

// GetName implements router.HandlerInfo interface
func (h *HandlerInfo) GetName() string {
	return h.Name
}

// GetPackage implements router.HandlerInfo interface
func (h *HandlerInfo) GetPackage() string {
	return h.PackageName
}

// GetRoute implements router.HandlerInfo interface
func (h *HandlerInfo) GetRoute() *router.RouteInfo {
	return h.Route
}

// SetRoute implements router.HandlerInfo interface
func (h *HandlerInfo) SetRoute(route *router.RouteInfo) {
	h.Route = route
}

// FileAnalysisResult represents the complete analysis of a Go source file
type FileAnalysisResult struct {
	// File path
	FilePath string

	// Package name
	PackageName string

	// All handlers found in the file
	Handlers []HandlerInfo

	// All anonymous structs that need to be replaced
	AnonymousStructs []AnonymousStructInfo

	// Raw AST file
	ASTFile *ast.File

	// File set for position information
	FileSet *token.FileSet
}

// TransformationPlan represents the plan for transforming a file
type TransformationPlan struct {
	// Original file analysis
	Analysis FileAnalysisResult

	// Types to be generated
	TypesToGenerate []TypeInfo

	// Replacements to be made
	Replacements []Replacement
}

// TypeInfo represents a named type to be generated
type TypeInfo struct {
	// Type name (e.g., "ProductsRequest")
	Name string

	// The struct type definition
	StructType *ast.StructType

	// Comment documentation
	DocComment string

	// Whether this is a request body, query params, etc.
	BindingType BindingType

	// Original handler name for context
	HandlerName string
}

// Replacement represents a replacement to be made in the source code
type Replacement struct {
	// Position of the text to replace
	Start token.Pos
	End   token.Pos

	// New text to insert
	NewText string

	// Description of what this replacement does
	Description string
}
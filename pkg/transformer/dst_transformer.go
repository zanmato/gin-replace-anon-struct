package transformer

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/andreas/gin-replace-anon-struct/pkg/analyzer"
	"github.com/andreas/gin-replace-anon-struct/pkg/namer"
	"github.com/andreas/gin-replace-anon-struct/pkg/swagger"
	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/dstutil"
)

// DSTTransformer handles DST-based transformations to replace anonymous structs with named types
type DSTTransformer struct {
	originalFileSet  *token.FileSet
	originalFile     *ast.File
	analysis         analyzer.FileAnalysisResult
	stripRoutePrefix string
	typeNames        map[string]string
	nameGenerator    *namer.TypeNameGenerator
}

// NewDSTTransformer creates a new DST transformer instance
func NewDSTTransformer(fileSet *token.FileSet, file *ast.File, analysis analyzer.FileAnalysisResult, stripRoutePrefix string) *DSTTransformer {
	return &DSTTransformer{
		originalFileSet:  fileSet,
		originalFile:     file,
		analysis:         analysis,
		stripRoutePrefix: stripRoutePrefix,
		typeNames:        make(map[string]string),
		nameGenerator:    namer.NewTypeNameGenerator(),
	}
}

// Transform performs the complete DST transformation
func (t *DSTTransformer) Transform() (string, error) {
	// Convert original AST to DST
	dstFile, err := decorator.DecorateFile(t.originalFileSet, t.originalFile)
	if err != nil {
		return "", fmt.Errorf("failed to convert AST to DST: %w", err)
	}

	// Update analysis with generated type names
	t.updateAnalysisWithGeneratedTypeNames()

	// Generate type declarations
	typeDecls := t.generateTypeDeclarations()

	// Insert type declarations at the top (after imports) - this preserves comments properly
	t.insertTypeDeclarationsAtTop(dstFile, typeDecls)

	// Replace anonymous structs with named types in function bodies
	t.replaceAnonymousStructs(dstFile)

	// Insert Swagger annotations
	t.insertSwaggerAnnotations(dstFile)

	// Print the transformed DST back to Go source
	var buf bytes.Buffer
	err = decorator.Fprint(&buf, dstFile)
	if err != nil {
		return "", fmt.Errorf("failed to print DST to source: %w", err)
	}

	result := buf.String()
	return result, nil
}

// updateAnalysisWithGeneratedTypeNames updates the analysis with generated type names
func (t *DSTTransformer) updateAnalysisWithGeneratedTypeNames() {
	// Collect existing type names to avoid conflicts
	var existingTypes []string
	for _, handler := range t.analysis.Handlers {
		existingTypes = append(existingTypes, handler.Name)
	}

	for i, anonStruct := range t.analysis.AnonymousStructs {
		key := fmt.Sprintf("%s:%s:%s", anonStruct.HandlerName, anonStruct.VariableName, anonStruct.BindingType)
		typeName, exists := t.typeNames[key]
		if !exists {
			// Generate a type name if it doesn't exist
			typeName = t.nameGenerator.GenerateTypeName(
				anonStruct.HandlerName,
				anonStruct.VariableName,
				anonStruct.BindingType,
				existingTypes,
			)
			t.typeNames[key] = typeName
			existingTypes = append(existingTypes, typeName)
		}
		// Update the GeneratedTypeName field in the analysis
		t.analysis.AnonymousStructs[i].GeneratedTypeName = typeName
	}
}

// generateTypeDeclarations creates type declarations for all anonymous structs
func (t *DSTTransformer) generateTypeDeclarations() []dst.Decl {
	var typeDecls []dst.Decl

	for i, anonStruct := range t.analysis.AnonymousStructs {
		key := fmt.Sprintf("%s:%s:%s", anonStruct.HandlerName, anonStruct.VariableName, anonStruct.BindingType)
		typeName, exists := t.typeNames[key]
		if !exists {
			continue
		}

		// Create a DST type declaration
		typeDecl := &dst.GenDecl{
			Tok: token.TYPE,
			Specs: []dst.Spec{
				&dst.TypeSpec{
					Name: &dst.Ident{
						Name: typeName,
					},
					Type: t.cloneStructType(anonStruct.StructType),
				},
			},
		}

		// Add proper spacing between type declarations
		if i > 0 {
			// Add a blank line before this type declaration (except for the first one)
			typeDecl.Decorations().Before = dst.EmptyLine
		}

		typeDecls = append(typeDecls, typeDecl)
	}

	return typeDecls
}

// cloneStructType creates a DST struct type from an AST struct type
func (t *DSTTransformer) cloneStructType(structType *ast.StructType) dst.Expr {
	if structType == nil {
		return &dst.Ident{Name: "interface{}"}
	}

	return &dst.StructType{
		Fields: t.cloneFieldList(structType.Fields),
	}
}

// cloneFieldList creates a DST field list from an AST field list
func (t *DSTTransformer) cloneFieldList(fieldList *ast.FieldList) *dst.FieldList {
	if fieldList == nil {
		return &dst.FieldList{}
	}

	clonedFields := make([]*dst.Field, len(fieldList.List))
	for i, field := range fieldList.List {
		clonedField := &dst.Field{
			Names: t.cloneIdents(field.Names),
			Type:  t.cloneExpr(field.Type),
			Tag:   t.cloneBasicLit(field.Tag),
		}

		// Add swaggertype:"string" tag to json.RawMessage fields
		t.addSwaggerTypeTag(clonedField)

		clonedFields[i] = clonedField
	}

	return &dst.FieldList{
		List: clonedFields,
	}
}

// cloneIdents creates a slice of DST identifiers from AST identifiers
func (t *DSTTransformer) cloneIdents(idents []*ast.Ident) []*dst.Ident {
	if len(idents) == 0 {
		return nil
	}

	cloned := make([]*dst.Ident, len(idents))
	for i, ident := range idents {
		cloned[i] = &dst.Ident{
			Name: ident.Name,
		}
	}

	return cloned
}

// cloneBasicLit creates a DST basic literal from an AST basic literal
func (t *DSTTransformer) cloneBasicLit(lit *ast.BasicLit) *dst.BasicLit {
	if lit == nil {
		return nil
	}
	return &dst.BasicLit{
		Kind:  lit.Kind,
		Value: lit.Value,
	}
}

// isJSONRawMessageType checks if an expression is json.RawMessage or *json.RawMessage
func (t *DSTTransformer) isJSONRawMessageType(expr ast.Expr) bool {
	if expr == nil {
		return false
	}

	switch e := expr.(type) {
	case *ast.Ident:
		// Check for json.RawMessage
		return e.Name == "RawMessage"
	case *ast.SelectorExpr:
		// Check for json.RawMessage
		if x, ok := e.X.(*ast.Ident); ok && x.Name == "json" && e.Sel.Name == "RawMessage" {
			return true
		}
	case *ast.StarExpr:
		// Check for *json.RawMessage
		return t.isJSONRawMessageType(e.X)
	}
	return false
}

// addSwaggerTypeTag adds swaggertype:"string" tag to json.RawMessage fields
func (t *DSTTransformer) addSwaggerTypeTag(field *dst.Field) {
	if field == nil || len(field.Names) == 0 {
		return
	}

	// Check if the field type is json.RawMessage
	if !t.isJSONRawMessageTypeToDST(field.Type) {
		return
	}

	// Get existing tag
	existingTag := ""
	if field.Tag != nil {
		existingTag = field.Tag.Value
	}

	// Parse existing tag and add swaggertype if not present
	newTag := t.addSwaggerTypeToTag(existingTag)
	if newTag != existingTag {
		field.Tag = &dst.BasicLit{
			Kind:  token.STRING,
			Value: newTag,
		}
	}
}

// isJSONRawMessageTypeToDST checks if a DST expression is json.RawMessage or *json.RawMessage
func (t *DSTTransformer) isJSONRawMessageTypeToDST(expr dst.Expr) bool {
	if expr == nil {
		return false
	}

	switch e := expr.(type) {
	case *dst.Ident:
		// Check for RawMessage (assuming json is imported)
		return e.Name == "RawMessage"
	case *dst.SelectorExpr:
		// Check for json.RawMessage
		if x, ok := e.X.(*dst.Ident); ok && x.Name == "json" && e.Sel.Name == "RawMessage" {
			return true
		}
	case *dst.StarExpr:
		// Check for *json.RawMessage
		return t.isJSONRawMessageTypeToDST(e.X)
	}
	return false
}

// addSwaggerTypeToTag adds swaggertype:"string" to an existing tag string
func (t *DSTTransformer) addSwaggerTypeToTag(tagStr string) string {
	// Remove backticks if present
	tagContent := strings.Trim(tagStr, "`")

	// Check if swaggertype is already present
	if strings.Contains(tagContent, `swaggertype:"array,object"`) {
		return tagStr
	}

	// Add swaggertype tag
	if tagContent == "" {
		tagContent = `swaggertype:"array,object"`
	} else {
		tagContent += ` swaggertype:"array,object"`
	}

	// Return with backticks
	return "`" + tagContent + "`"
}

// cloneExpr creates a DST expression from an AST expression
func (t *DSTTransformer) cloneExpr(expr ast.Expr) dst.Expr {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *ast.Ident:
		return &dst.Ident{Name: e.Name}
	case *ast.SelectorExpr:
		return &dst.SelectorExpr{
			X:   t.cloneExpr(e.X),
			Sel: &dst.Ident{Name: e.Sel.Name},
		}
	case *ast.StarExpr:
		return &dst.StarExpr{
			X: t.cloneExpr(e.X),
		}
	case *ast.ArrayType:
		return &dst.ArrayType{
			Elt: t.cloneExpr(e.Elt),
			Len: t.cloneExpr(e.Len),
		}
	case *ast.MapType:
		return &dst.MapType{
			Key:   t.cloneExpr(e.Key),
			Value: t.cloneExpr(e.Value),
		}
	case *ast.InterfaceType:
		return &dst.InterfaceType{
			Methods: t.cloneFieldList(e.Methods),
		}
	case *ast.StructType:
		return &dst.StructType{
			Fields: t.cloneFieldList(e.Fields),
		}
	case *ast.FuncType:
		return &dst.FuncType{
			Params:  t.cloneFieldList(e.Params),
			Results: t.cloneFieldList(e.Results),
		}
	case *ast.Ellipsis:
		return &dst.Ellipsis{
			Elt: t.cloneExpr(e.Elt),
		}
	case *ast.BasicLit:
		return t.cloneBasicLit(e)
	case *ast.ParenExpr:
		return &dst.ParenExpr{
			X: t.cloneExpr(e.X),
		}
	default:
		// For unsupported types, fall back to interface{}
		return &dst.Ident{Name: "interface{}"}
	}
}

// insertTypeDeclarationsAtTop inserts type declarations at the top of the file after imports
func (t *DSTTransformer) insertTypeDeclarationsAtTop(dstFile *dst.File, typeDecls []dst.Decl) {
	if len(typeDecls) == 0 {
		return
	}

	// Find the insertion point after imports
	insertPos := 0
	foundImport := false
	for i, decl := range dstFile.Decls {
		if genDecl, ok := decl.(*dst.GenDecl); ok {
			if genDecl.Tok == token.IMPORT {
				insertPos = i + 1
				foundImport = true
			} else if genDecl.Tok == token.TYPE {
				// Found an existing type declaration, insert before it
				break
			} else if genDecl.Tok == token.VAR || genDecl.Tok == token.CONST {
				// Found var/const declaration, insert before it
				break
			} else if foundImport {
				// Found non-import declaration after imports, this is where we should insert
				break
			}
		} else if foundImport {
			// Found non-GenDecl (like function) after imports, this is where we should insert
			break
		}
	}

	// Insert the type declarations
	newDecls := make([]dst.Decl, 0, len(dstFile.Decls)+len(typeDecls))

	// Add declarations up to insertion point
	newDecls = append(newDecls, dstFile.Decls[:insertPos]...)

	// Add the new type declarations
	newDecls = append(newDecls, typeDecls...)

	// Add the remaining declarations
	newDecls = append(newDecls, dstFile.Decls[insertPos:]...)

	dstFile.Decls = newDecls
}

// replaceAnonymousStructs replaces anonymous structs with named types in the DST
func (t *DSTTransformer) replaceAnonymousStructs(dstFile *dst.File) {
	dstutil.Apply(dstFile, func(cursor *dstutil.Cursor) bool {
		// Look for function declarations
		funcDecl, ok := cursor.Node().(*dst.FuncDecl)
		if !ok {
			return true
		}

		// Get the function name
		funcName := funcDecl.Name.Name

		// Check if this function has anonymous structs to replace
		for _, anonStruct := range t.analysis.AnonymousStructs {
			handlerFuncName := anonStruct.HandlerName
			if dotIndex := strings.LastIndex(handlerFuncName, "."); dotIndex != -1 {
				handlerFuncName = handlerFuncName[dotIndex+1:]
			}

			if handlerFuncName == funcName {
				// Replace anonymous structs in this function
				t.replaceAnonymousStructsInFunction(funcDecl, funcName)
				break
			}
		}

		return true
	}, nil)
}

// replaceAnonymousStructsInFunction replaces anonymous structs within a function declaration
func (t *DSTTransformer) replaceAnonymousStructsInFunction(funcDecl *dst.FuncDecl, funcName string) {
	dstutil.Apply(funcDecl, func(cursor *dstutil.Cursor) bool {
		switch node := cursor.Node().(type) {
		// Look for value specs (variable declarations)
		case *dst.ValueSpec:
			var structType *dst.StructType
			var isArray bool

			// Check if this value spec has an anonymous struct type (direct or in array)
			switch typ := node.Type.(type) {
			case *dst.StructType:
				// Direct struct type: var req struct{}
				structType = typ
				isArray = false
			case *dst.ArrayType:
				// Array of structs: var res []struct{}
				if st, ok := typ.Elt.(*dst.StructType); ok {
					structType = st
					isArray = true
				}
			}

			if structType == nil {
				return true
			}

			// This is an anonymous struct in a variable declaration
			// Find the appropriate type name for this struct
			typeName := t.findTypeNameForValueSpec(node, structType, funcName)
			if typeName != "" {
				// Replace the struct type with the named type identifier
				if isArray {
					// For array types, replace the element type
					if arrayType, ok := node.Type.(*dst.ArrayType); ok {
						arrayType.Elt = &dst.Ident{Name: typeName}
					}
				} else {
					// For direct struct types, replace the entire type
					node.Type = &dst.Ident{Name: typeName}
				}
			}

		// Look for assignment statements
		case *dst.AssignStmt:
			t.replaceAssignmentStructType(node)

		// Look for call expressions (c.JSON calls)
		case *dst.CallExpr:
			t.replaceCallExprStructType(node)
		}

		return true
	}, nil)
}

// findTypeNameForValueSpec finds the appropriate type name for a value spec containing an anonymous struct
func (t *DSTTransformer) findTypeNameForValueSpec(valueSpec *dst.ValueSpec, structType *dst.StructType, funcName string) string {
	// Get the variable name
	varName := ""
	if len(valueSpec.Names) > 0 {
		varName = valueSpec.Names[0].Name
	}

	// Count the number of fields in this struct
	fieldCount := 0
	if structType.Fields != nil {
		fieldCount = len(structType.Fields.List)
	}

	// Find matching anonymous struct from analysis based on function name, variable name, and field count
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.GeneratedTypeName != "" && anonStruct.StructType != nil {
			// Check if this matches the handler and variable
			handlerFuncName := anonStruct.HandlerName
			if dotIndex := strings.LastIndex(handlerFuncName, "."); dotIndex != -1 {
				handlerFuncName = handlerFuncName[dotIndex+1:]
			}

			if handlerFuncName == funcName && anonStruct.VariableName == varName {
				return anonStruct.GeneratedTypeName
			}
		}
	}

	// Fallback: try to match by field count and handler name (more specific than just field count)
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.GeneratedTypeName != "" && anonStruct.StructType != nil {
			anonFieldCount := 0
			if anonStruct.StructType.Fields != nil {
				anonFieldCount = len(anonStruct.StructType.Fields.List)
			}

			// Match by field count AND handler name to reduce false positives
			if anonFieldCount == fieldCount {
				handlerFuncName := anonStruct.HandlerName
				if dotIndex := strings.LastIndex(handlerFuncName, "."); dotIndex != -1 {
					handlerFuncName = handlerFuncName[dotIndex+1:]
				}

				if handlerFuncName == funcName {
					return anonStruct.GeneratedTypeName
				}
			}
		}
	}

	// Last resort: match by field count only (could pick wrong type, but better than nothing)
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.GeneratedTypeName != "" && anonStruct.StructType != nil {
			anonFieldCount := 0
			if anonStruct.StructType.Fields != nil {
				anonFieldCount = len(anonStruct.StructType.Fields.List)
			}
			if anonFieldCount == fieldCount {
				return anonStruct.GeneratedTypeName
			}
		}
	}

	return ""
}

// replaceAssignmentStructType replaces struct type in assignment statements
func (t *DSTTransformer) replaceAssignmentStructType(assignStmt *dst.AssignStmt) {
	// Look for assignments with anonymous structs on the RHS
	for i, rhs := range assignStmt.Rhs {
		if compLit, ok := rhs.(*dst.CompositeLit); ok {
			if _, ok := compLit.Type.(*dst.StructType); ok {
				// Find the anonymous struct info for this assignment
				if typeName := t.findTypeNameForAssignment(assignStmt, i); typeName != "" {
					// Replace the struct type with named type
					compLit.Type = &dst.Ident{Name: typeName}
				}
			} else if arrayType, ok := compLit.Type.(*dst.ArrayType); ok {
				// Handle slice of structs: items := []struct{}{}
				if _, ok := arrayType.Elt.(*dst.StructType); ok {
					// Find the anonymous struct info for this assignment
					if typeName := t.findTypeNameForAssignment(assignStmt, i); typeName != "" {
						// Replace the element type with named type: []struct{} -> []TypeName
						arrayType.Elt = &dst.Ident{Name: typeName}
					}
				}
			}
		}
	}
}

// findTypeNameForAssignment finds the type name for an assignment containing an anonymous struct
func (t *DSTTransformer) findTypeNameForAssignment(assignStmt *dst.AssignStmt, rhsIndex int) string {
	// Look through anonymous structs to find a match
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.AssignmentStmt != nil {
			// Check if this is the right side of the assignment
			if rhsIndex < len(assignStmt.Rhs) {
				// Find the variable name on the LHS
				if rhsIndex < len(assignStmt.Lhs) {
					if ident, ok := assignStmt.Lhs[rhsIndex].(*dst.Ident); ok {
						if ident.Name == anonStruct.VariableName {
							key := fmt.Sprintf("%s:%s:%s", anonStruct.HandlerName, anonStruct.VariableName, anonStruct.BindingType)
							if typeName, exists := t.typeNames[key]; exists {
								return typeName
							}
						}
					}
				}
			}
		}
	}
	return ""
}

// replaceCallExprStructType replaces struct types in call expressions (c.JSON calls)
func (t *DSTTransformer) replaceCallExprStructType(callExpr *dst.CallExpr) {
	// Check if this is a c.JSON call
	if len(callExpr.Args) < 2 {
		return
	}

	sel, ok := callExpr.Fun.(*dst.SelectorExpr)
	if !ok {
		return
	}

	// Check if it's c.JSON or c.XML (common Gin response methods)
	if sel.Sel.Name != "JSON" && sel.Sel.Name != "XML" {
		return
	}

	// Look at the second argument (the data being sent)
	dataArg := callExpr.Args[1]

	// Handle composite literals with anonymous structs
	if compLit, ok := dataArg.(*dst.CompositeLit); ok {
		if _, ok := compLit.Type.(*dst.StructType); ok {
			// This is an anonymous struct being sent to c.JSON
			// Find the appropriate type name for this struct
			typeName := t.findTypeNameForCallExpr(compLit)
			if typeName != "" {
				// Replace the struct type with named type
				compLit.Type = &dst.Ident{Name: typeName}
			}
		} else if arrayType, ok := compLit.Type.(*dst.ArrayType); ok {
			// Handle slice of structs: c.JSON(200, []struct{}{...})
			if _, ok := arrayType.Elt.(*dst.StructType); ok {
				typeName := t.findTypeNameForCallExpr(compLit)
				if typeName != "" {
					// Replace the element type with named type: []struct{} -> []TypeName
					arrayType.Elt = &dst.Ident{Name: typeName}
				}
			}
		}
	}

	// Handle gin.H maps - check if the first argument is a gin.H literal
	if len(callExpr.Args) >= 2 {
		if selExpr, ok := callExpr.Args[1].(*dst.CompositeLit); ok {
			if ident, ok := selExpr.Type.(*dst.Ident); ok && ident.Name == "gin.H" {
				// This is a gin.H being used in c.JSON, find the response type for this handler
				typeName := t.findResponseTypeNameForHandler(callExpr)
				if typeName != "" {
					// Replace gin.H with the named response type
					selExpr.Type = &dst.Ident{Name: typeName}
				}
			}
		}
	}
}

// findTypeNameForCallExpr finds the type name for a call expression containing an anonymous struct
func (t *DSTTransformer) findTypeNameForCallExpr(compLit *dst.CompositeLit) string {
	// Count the number of fields in this struct
	fieldCount := 0
	if structType, ok := compLit.Type.(*dst.StructType); ok && structType.Fields != nil {
		fieldCount = len(structType.Fields.List)
	}

	// Try to find a matching anonymous struct from analysis by field count
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.GeneratedTypeName != "" && anonStruct.StructType != nil {
			anonFieldCount := 0
			if anonStruct.StructType.Fields != nil {
				anonFieldCount = len(anonStruct.StructType.Fields.List)
			}
			if anonFieldCount == fieldCount {
				return anonStruct.GeneratedTypeName
			}
		}
	}

	return ""
}

// findResponseTypeNameForHandler finds the response type name for a handler containing a gin.H
func (t *DSTTransformer) findResponseTypeNameForHandler(callExpr *dst.CallExpr) string {
	// We need to find the enclosing function to determine which handler this c.JSON call belongs to
	// Since we're in DST context, we'll use a different approach than the AST transformer

	// Try to find response types created from gin.H in the analysis
	// These will have BindingType == "response" and will have been created from gin.H literals
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.BindingType == "response" && anonStruct.GeneratedTypeName != "" {
			// Check if this was created from a gin.H (we can identify this by checking if it has the right characteristics)
			// gin.H-derived structs typically have simple field names and are used as responses
			if t.isLikelyGinHDerivedResponse(anonStruct) {
				return anonStruct.GeneratedTypeName
			}
		}
	}

	// Fallback: return any response type
	for _, anonStruct := range t.analysis.AnonymousStructs {
		if anonStruct.BindingType == "response" && anonStruct.GeneratedTypeName != "" {
			return anonStruct.GeneratedTypeName
		}
	}
	return ""
}

// isLikelyGinHDerivedResponse checks if an anonymous struct was likely created from a gin.H literal
func (t *DSTTransformer) isLikelyGinHDerivedResponse(anonStruct analyzer.AnonymousStructInfo) bool {
	// gin.H-derived responses typically have these characteristics:
	// 1. They are response types
	// 2. They have simple field names (often single words, TitleCase or camelCase)
	// 3. They were created in the detector from gin.H literals

	if anonStruct.StructType == nil || anonStruct.StructType.Fields == nil {
		return false
	}

	// Check if the field names look like they came from gin.H keys
	// gin.H keys are typically strings, so the struct fields would be TitleCase versions
	for _, field := range anonStruct.StructType.Fields.List {
		if len(field.Names) > 0 {
			fieldName := field.Names[0].Name
			// gin.H keys are typically converted to TitleCase for struct fields
			// If the field name looks like a TitleCase conversion of a string key, it's likely from gin.H
			if !t.looksLikeGinHDerivedField(fieldName) {
				return false
			}
		}
	}

	return true
}

// looksLikeGinHDerivedField checks if a field name looks like it came from a gin.H key
func (t *DSTTransformer) looksLikeGinHDerivedField(fieldName string) bool {
	// gin.H keys are typically:
	// - Simple strings like "error", "id", "name", "created_at"
	// - Converted to TitleCase for struct fields: "Error", "ID", "Name", "CreatedAt"

	// Common patterns that suggest gin.H origin:
	if len(fieldName) == 0 {
		return false
	}

	// All caps like ID, URL, etc. are common in gin.H responses
	if fieldName == strings.ToUpper(fieldName) && len(fieldName) <= 5 {
		return true
	}

	// TitleCase with common suffixes like Id, Name, Error, etc.
	if strings.HasSuffix(fieldName, "Id") || strings.HasSuffix(fieldName, "ID") ||
		strings.HasSuffix(fieldName, "Name") || strings.HasSuffix(fieldName, "Error") ||
		strings.HasSuffix(fieldName, "At") || strings.HasSuffix(fieldName, "Count") {
		return true
	}

	// Simple TitleCase words
	if len(fieldName) > 0 && fieldName[0] == strings.ToUpper(string(fieldName[0]))[0] {
		// Check if it's a simple TitleCase word (not too complex)
		if len(fieldName) <= 20 && !strings.Contains(fieldName, "_") {
			return true
		}
	}

	return false
}

// insertSwaggerAnnotations inserts Swagger annotations above handler functions
func (t *DSTTransformer) insertSwaggerAnnotations(dstFile *dst.File) {
	// Create Swagger annotation generator
	generator := swagger.NewAnnotationGenerator(t.analysis, t.stripRoutePrefix)
	t.updateAnalysisWithGeneratedTypeNames()

	// Generate annotations for all handlers
	annotations := generator.GenerateAnnotations()

	// Apply annotations to the AST
	for _, handlerAnnotation := range annotations {
		t.applyHandlerAnnotations(dstFile, handlerAnnotation)
	}
}

// applyHandlerAnnotations applies Swagger annotations to a handler function in DST
func (t *DSTTransformer) applyHandlerAnnotations(dstFile *dst.File, handlerAnnotation swagger.HandlerAnnotations) {
	if len(handlerAnnotation.Annotations) == 0 {
		return
	}

	// Extract function name
	funcName := handlerAnnotation.HandlerName
	if dotIndex := strings.LastIndex(funcName, "."); dotIndex != -1 {
		funcName = funcName[dotIndex+1:]
	}

	// Find the function by name in the DST
	dstutil.Apply(dstFile, func(cursor *dstutil.Cursor) bool {
		funcDecl, ok := cursor.Node().(*dst.FuncDecl)
		if !ok {
			return true
		}

		if funcDecl.Name.Name == funcName {
			// Add Swagger annotations as comments before the function
			t.addSwaggerCommentsBeforeFunction(funcDecl, handlerAnnotation.Annotations)
		}

		return true
	}, nil)
}

// addSwaggerCommentsBeforeFunction adds Swagger comments before a function declaration
func (t *DSTTransformer) addSwaggerCommentsBeforeFunction(funcDecl *dst.FuncDecl, annotations []string) {
	// Only add Swagger annotations, don't duplicate existing doc comments
	// The DST decorator already preserves existing doc comments

	// Create comment strings with Swagger annotations
	var commentStrings []string
	for _, annotation := range annotations {
		commentStrings = append(commentStrings, "// "+annotation)
	}

	// Add the comments as decorations to the function declaration
	// Use Start decorations to add comments before the function
	for _, comment := range commentStrings {
		funcDecl.Decorations().Start.Append(comment)
	}
}

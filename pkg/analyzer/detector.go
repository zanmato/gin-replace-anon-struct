package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"unicode"

	"github.com/andreas/gin-replace-anon-struct/pkg/router"
)

// AnonymousStructDetector identifies anonymous structs in Gin handlers
type AnonymousStructDetector struct {
	fileSet  *token.FileSet
	file     *ast.File
	pkgName  string
	handlers map[string]*HandlerInfo
}

// NewAnonymousStructDetector creates a new detector instance
func NewAnonymousStructDetector(fileSet *token.FileSet, file *ast.File, pkgName string) *AnonymousStructDetector {
	return &AnonymousStructDetector{
		fileSet:  fileSet,
		file:     file,
		pkgName:  pkgName,
		handlers: make(map[string]*HandlerInfo),
	}
}

// DetectAnonymousStructsWithRouter finds all anonymous structs in Gin handlers with router info
func (d *AnonymousStructDetector) DetectAnonymousStructsWithRouter(routerFilePath, stripRoutePrefix string) (FileAnalysisResult, error) {
	// First pass: identify all Gin handlers
	d.identifyHandlers()

	// Second pass: find anonymous structs in handlers
	d.findAnonymousStructs()

	// Third pass: add route information from router file (if provided)
	if routerFilePath != "" {
		err := d.addRouteInformationFromFile(routerFilePath, stripRoutePrefix)
		if err != nil {
			// Error out if router parsing fails - no fallback allowed
			return FileAnalysisResult{}, fmt.Errorf("failed to parse router file %s: %w", routerFilePath, err)
		}
	} else {
		// Error out if no router file provided - no fallback allowed
		return FileAnalysisResult{}, fmt.Errorf("no router file provided - route inference fallback has been disabled")
	}

	// Collect all anonymous structs
	var allAnonymousStructs []AnonymousStructInfo
	for _, handler := range d.handlers {
		allAnonymousStructs = append(allAnonymousStructs, handler.AnonymousStructs...)
	}

	// Convert handlers map to slice - include all handlers (even those without anonymous structs)
	var handlerSlice []HandlerInfo
	for _, handler := range d.handlers {
		handlerSlice = append(handlerSlice, *handler)
	}

	return FileAnalysisResult{
		FilePath:         d.fileSet.Position(d.file.Pos()).Filename,
		PackageName:      d.pkgName,
		Handlers:         handlerSlice,
		AnonymousStructs: allAnonymousStructs,
		ASTFile:          d.file,
		FileSet:          d.fileSet,
	}, nil
}

// identifyHandlers finds all Gin handler functions in the file
func (d *AnonymousStructDetector) identifyHandlers() {
	ast.Inspect(d.file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if d.isGinHandler(node) {
				handlerName := node.Name.Name
				handler := &HandlerInfo{
					Name:             handlerName,
					FuncDecl:         node,
					IsFactory:        false,
					PackageName:      d.pkgName,
					AnonymousStructs: []AnonymousStructInfo{},
					DocComment:       d.extractDocComment(node),
				}
				d.handlers[handlerName] = handler
			} else if d.isHandlerFactory(node) {
				handlerName := node.Name.Name
				handler := &HandlerInfo{
					Name:             handlerName,
					FuncDecl:         node,
					IsFactory:        true,
					PackageName:      d.pkgName,
					AnonymousStructs: []AnonymousStructInfo{},
					DocComment:       d.extractDocComment(node),
				}
				d.handlers[handlerName] = handler
			}
		}
		return true
	})
}

// isGinHandler checks if a function is a Gin handler (takes *gin.Context)
func (d *AnonymousStructDetector) isGinHandler(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}

	for _, param := range fn.Type.Params.List {
		if starExpr, ok := param.Type.(*ast.StarExpr); ok {
			if selExpr, ok := starExpr.X.(*ast.SelectorExpr); ok {
				if x, ok := selExpr.X.(*ast.Ident); ok {
					return x.Name == "gin" && selExpr.Sel.Name == "Context"
				}
			}
		}
	}
	return false
}

// isHandlerFactory checks if a function returns gin.HandlerFunc
func (d *AnonymousStructDetector) isHandlerFactory(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}

	for _, result := range fn.Type.Results.List {
		if selExpr, ok := result.Type.(*ast.SelectorExpr); ok {
			if x, ok := selExpr.X.(*ast.Ident); ok {
				return x.Name == "gin" && selExpr.Sel.Name == "HandlerFunc"
			}
		}
	}
	return false
}

// findInnerHandlerInFactory looks for function literals in handler factories
func (d *AnonymousStructDetector) findInnerHandlerInFactory(fn *ast.FuncDecl, factory *HandlerInfo) {
	if fn.Body == nil {
		return
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if ret, ok := n.(*ast.ReturnStmt); ok {
			for _, expr := range ret.Results {
				if funcLit, ok := expr.(*ast.FuncLit); ok {
					if d.isFuncLitGinHandler(funcLit) {
						// Find anonymous structs in the inner function and associate them with the factory
						d.findAnonymousStructsInBody(funcLit.Body, factory)
					}
				}
			}
		}
		return true
	})
}

// isFuncLitGinHandler checks if a function literal takes *gin.Context
func (d *AnonymousStructDetector) isFuncLitGinHandler(funcLit *ast.FuncLit) bool {
	if funcLit.Type.Params == nil || len(funcLit.Type.Params.List) == 0 {
		return false
	}

	for _, param := range funcLit.Type.Params.List {
		if starExpr, ok := param.Type.(*ast.StarExpr); ok {
			if selExpr, ok := starExpr.X.(*ast.SelectorExpr); ok {
				if x, ok := selExpr.X.(*ast.Ident); ok {
					return x.Name == "gin" && selExpr.Sel.Name == "Context"
				}
			}
		}
	}
	return false
}

// findAnonymousStructs searches for anonymous structs in all handlers
func (d *AnonymousStructDetector) findAnonymousStructs() {
	for _, handler := range d.handlers {
		if handler.FuncDecl != nil && handler.FuncDecl.Body != nil {
			// For factory functions, we don't want to process the factory body itself,
			// only the inner function literal that will be found by findInnerHandlerInFactory
			if !handler.IsFactory {
				d.findAnonymousStructsInBody(handler.FuncDecl.Body, handler)
			}
		}
		// Process factory inner functions
		if handler.IsFactory && handler.FuncDecl != nil {
			d.findInnerHandlerInFactory(handler.FuncDecl, handler)
		}
		// Also find response structs
		d.findResponseStructs(handler)
	}
}

// findAnonymousStructsInBody finds anonymous structs in a function body
func (d *AnonymousStructDetector) findAnonymousStructsInBody(body *ast.BlockStmt, handler *HandlerInfo) {
	// Track variable declarations for anonymous structs
	varDecls := make(map[string]*ast.ValueSpec)

	// Track positions we've already detected to avoid duplicates
	detectedPositions := make(map[token.Pos]bool)

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			// Track variable declarations: var req struct{} or var pt []struct{}
			for _, name := range node.Names {
				if node.Type != nil {
					var structType *ast.StructType
					// Check for direct struct type: var req struct{}
					if st, ok := node.Type.(*ast.StructType); ok {
						structType = st
					} else if arrayType, ok := node.Type.(*ast.ArrayType); ok {
						// Check for slice of structs: var pt []struct{}
						if st, ok := arrayType.Elt.(*ast.StructType); ok {
							structType = st
						}
					}

					if structType != nil {
						// Check if we've already detected this position to avoid duplicates
						if !detectedPositions[node.Pos()] {
							varDecls[name.Name] = node
							// Check if this is an array type
							isArray := false
							if _, ok := node.Type.(*ast.ArrayType); ok {
								isArray = true
							}
							// Found anonymous struct in var declaration
							anonymousStruct := AnonymousStructInfo{
								VariableName:      name.Name,
								HandlerName:       handler.Name,
								BindingType:       BindingTypeJSON, // Default, will be updated
								StructType:        structType,
								Position:          node.Pos(),
								ValueSpec:         node,
								IsArray:           isArray,
								GeneratedTypeName: "",
							}
							handler.AnonymousStructs = append(handler.AnonymousStructs, anonymousStruct)
							detectedPositions[node.Pos()] = true
						}
					}
				}
			}

		case *ast.AssignStmt:
			// Handle assignment: q := struct{...}{} or items := []struct{...}{}
			for i, lhs := range node.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && i < len(node.Rhs) {
					if compLit, ok := node.Rhs[i].(*ast.CompositeLit); ok {
						var structType *ast.StructType
						// Check for direct struct type: q := struct{...}{}
						if st, ok := compLit.Type.(*ast.StructType); ok {
							structType = st
						} else if arrayType, ok := compLit.Type.(*ast.ArrayType); ok {
							// Check for slice of structs: items := []struct{...}{}
							if st, ok := arrayType.Elt.(*ast.StructType); ok {
								structType = st
							}
						}

						if structType != nil {
							// Check if this is an array type
							isArray := false
							if _, ok := compLit.Type.(*ast.ArrayType); ok {
								isArray = true
							}
							// Found anonymous struct in assignment
							anonymousStruct := AnonymousStructInfo{
								VariableName:      ident.Name,
								HandlerName:       handler.Name,
								BindingType:       BindingTypeJSON, // Default, will be updated
								StructType:        structType,
								Position:          node.Pos(),
								AssignmentStmt:    node,
								IsArray:           isArray,
								GeneratedTypeName: "",
							}
							handler.AnonymousStructs = append(handler.AnonymousStructs, anonymousStruct)
						}
					}
				}
			}

		case *ast.CallExpr:
			// Look for binding calls to determine binding type
			d.identifyBindingCalls(node, handler, varDecls)
		}
		return true
	})
}

// identifyBindingCalls identifies binding calls and updates anonymous structs with binding type
func (d *AnonymousStructDetector) identifyBindingCalls(call *ast.CallExpr, handler *HandlerInfo, varDecls map[string]*ast.ValueSpec) {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		bindingMethods := map[string]BindingType{
			"Bind":            BindingTypeJSON,
			"BindJSON":        BindingTypeJSON,
			"BindQuery":       BindingTypeQuery,
			"ShouldBind":      BindingTypeBind,
			"ShouldBindJSON":  BindingTypeJSON,
			"ShouldBindQuery": BindingTypeQuery,
		}

		if bindingType, exists := bindingMethods[sel.Sel.Name]; exists {
			// Check if this call takes a reference to a variable
			for _, arg := range call.Args {
				if unary, ok := arg.(*ast.UnaryExpr); ok && unary.Op == token.AND {
					if ident, ok := unary.X.(*ast.Ident); ok {
						// First, try to update anonymous structs
						foundAnonStruct := false
						for i, anonStruct := range handler.AnonymousStructs {
							if anonStruct.VariableName == ident.Name {
								handler.AnonymousStructs[i].BindingType = bindingType
								handler.AnonymousStructs[i].BindingCall = call
								foundAnonStruct = true
								break
							}
						}

						// If not found in anonymous structs, check if it's a named type
						if !foundAnonStruct {
							// Try to determine the type name from the variable declaration
							typeName := d.getVariableTypeName(ident, handler)
							if typeName != "" {
								namedBinding := NamedTypeBindingInfo{
									VariableName: ident.Name,
									TypeName:     typeName,
									BindingType:  bindingType,
									BindingCall:  call,
									Position:     call.Pos(),
								}
								handler.NamedTypeBindings = append(handler.NamedTypeBindings, namedBinding)
							}
						}
					}
				}
			}
		}
	}
}

// getVariableTypeName tries to determine the type name of a variable from its declaration
func (d *AnonymousStructDetector) getVariableTypeName(ident *ast.Ident, handler *HandlerInfo) string {
	if handler.FuncDecl == nil || handler.FuncDecl.Body == nil {
		return ""
	}

	// Search for the variable declaration in the function body
	var typeName string
	ast.Inspect(handler.FuncDecl.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.ValueSpec:
			// Check variable declarations: var a AccountAddress
			for _, name := range node.Names {
				if name.Name == ident.Name && node.Type != nil {
					typeName = d.getTypeName(node.Type)
					return false // Found it, stop searching
				}
			}
		case *ast.AssignStmt:
			// Check short variable declarations: a := AccountAddress{} or a := SomeFunction()
			for i, lhs := range node.Lhs {
				if lhsIdent, ok := lhs.(*ast.Ident); ok && lhsIdent.Name == ident.Name && i < len(node.Rhs) {
					// Check if the RHS is a function call or composite literal
					if compLit, ok := node.Rhs[i].(*ast.CompositeLit); ok {
						typeName = d.getTypeName(compLit.Type)
					} else if _, ok := node.Rhs[i].(*ast.CallExpr); ok {
						// For function calls, we'd need more sophisticated analysis
						// For now, just return empty
						typeName = ""
					}
					return false // Found it, stop searching
				}
			}
		}
		return true
	})

	return typeName
}

// getTypeName extracts the type name from an AST expression
func (d *AnonymousStructDetector) getTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return d.getTypeName(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return "*" + d.getTypeName(t.X)
	case *ast.ArrayType:
		return "[]" + d.getTypeName(t.Elt)
	case *ast.MapType:
		return "map[" + d.getTypeName(t.Key) + "]" + d.getTypeName(t.Value)
	default:
		return ""
	}
}

// findResponseStructs finds anonymous response structs in c.JSON calls
func (d *AnonymousStructDetector) findResponseStructs(handler *HandlerInfo) {
	if handler.FuncDecl == nil || handler.FuncDecl.Body == nil {
		return
	}

	// Find the last c.JSON call in the handler
	lastJSONCall := d.findLastJSONCall(handler.FuncDecl.Body)
	if lastJSONCall == nil {
		return
	}

	// Extract response struct from the c.JSON call
	d.extractResponseStructFromJSON(lastJSONCall, handler)
}

// findLastJSONCall finds the last c.JSON call in a function body
func (d *AnonymousStructDetector) findLastJSONCall(body *ast.BlockStmt) *ast.CallExpr {
	var lastJSONCall *ast.CallExpr

	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if sel.Sel.Name == "JSON" && len(call.Args) >= 2 {
					// Check if this is c.JSON (context.JSON)
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "c" {
						lastJSONCall = call
					}
				}
			}
		}
		return true
	})

	return lastJSONCall
}

// extractResponseStructFromJSON extracts response struct information from c.JSON call
func (d *AnonymousStructDetector) extractResponseStructFromJSON(jsonCall *ast.CallExpr, handler *HandlerInfo) {
	if len(jsonCall.Args) < 2 {
		return
	}

	responseArg := jsonCall.Args[1]

	switch arg := responseArg.(type) {
	case *ast.CompositeLit:
		// Handle anonymous struct literals: c.JSON(200, struct{ ID string }{ID: "123"})
		if structType, ok := arg.Type.(*ast.StructType); ok {
			responseStruct := AnonymousStructInfo{
				VariableName:   "response", // Default name for inline response structs
				HandlerName:    handler.Name,
				BindingType:    BindingTypeResponse,
				StructType:     structType,
				Position:       arg.Pos(),
				ResponseCall:   jsonCall,
				IsResponse:     true,
				HTTPStatusCode: d.extractHTTPStatusCode(jsonCall),
			}
			handler.AnonymousStructs = append(handler.AnonymousStructs, responseStruct)
		}

		// Handle gin.H literals: c.JSON(200, gin.H{"key": "value"})
		if sel, ok := arg.Type.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "gin" && sel.Sel.Name == "H" {
				// Convert gin.H to a struct with appropriate fields
				responseStruct := d.createResponseStructFromGinH(arg, handler, jsonCall)
				if responseStruct != nil {
					handler.AnonymousStructs = append(handler.AnonymousStructs, *responseStruct)
				}
			}
		}

		// Handle array/slice literals: c.JSON(200, []Product{})
		if arrayType, ok := arg.Type.(*ast.ArrayType); ok {
			if elementType, ok := arrayType.Elt.(*ast.Ident); ok {
				// This is a named array type, track it for @Success annotation
				namedResponse := NamedTypeResponseInfo{
					VariableName:   "response", // Default name for literal responses
					TypeName:       "[]" + elementType.Name,
					IsArray:        true,
					HTTPStatusCode: d.extractHTTPStatusCode(jsonCall),
					ResponseCall:   jsonCall,
					Position:       arg.Pos(),
				}
				handler.NamedTypeResponses = append(handler.NamedTypeResponses, namedResponse)
			}
		}

		// Handle named struct literals: c.JSON(200, Product{})
		if ident, ok := arg.Type.(*ast.Ident); ok {
			// This is a named type, track it for @Success annotation
			namedResponse := NamedTypeResponseInfo{
				VariableName:   "response", // Default name for literal responses
				TypeName:       ident.Name,
				IsArray:        false,
				HTTPStatusCode: d.extractHTTPStatusCode(jsonCall),
				ResponseCall:   jsonCall,
				Position:       arg.Pos(),
			}
			handler.NamedTypeResponses = append(handler.NamedTypeResponses, namedResponse)
		}

	case *ast.Ident:
		// Handle variables: c.JSON(200, p) where p might be an anonymous struct or named type
		// First, look through our already found anonymous structs to see if this matches
		foundAnonStruct := false
		for _, anonStruct := range handler.AnonymousStructs {
			if anonStruct.VariableName == arg.Name && !anonStruct.IsResponse {
				// This is a variable containing an anonymous struct used in response
				responseStruct := AnonymousStructInfo{
					VariableName:   anonStruct.VariableName,
					HandlerName:    handler.Name,
					BindingType:    BindingTypeResponse,
					StructType:     anonStruct.StructType,
					Position:       arg.Pos(),
					ValueSpec:      anonStruct.ValueSpec,
					AssignmentStmt: anonStruct.AssignmentStmt,
					ResponseCall:   jsonCall,
					IsResponse:     true,
					HTTPStatusCode: d.extractHTTPStatusCode(jsonCall),
					IsArray:        anonStruct.IsArray,
				}
				// Replace the original with the response version
				for i, existing := range handler.AnonymousStructs {
					if existing.VariableName == arg.Name && !existing.IsResponse {
						handler.AnonymousStructs[i] = responseStruct
						foundAnonStruct = true
						break
					}
				}
				break
			}
		}

		// If not found as anonymous struct, check if it's a named type variable
		if !foundAnonStruct {
			typeName := d.getVariableTypeName(arg, handler)
			if typeName != "" {
				// Determine if it's an array type
				isArray := false
				if strings.HasPrefix(typeName, "[]") {
					isArray = true
				}

				namedResponse := NamedTypeResponseInfo{
					VariableName:   arg.Name,
					TypeName:       typeName,
					IsArray:        isArray,
					HTTPStatusCode: d.extractHTTPStatusCode(jsonCall),
					ResponseCall:   jsonCall,
					Position:       arg.Pos(),
				}
				handler.NamedTypeResponses = append(handler.NamedTypeResponses, namedResponse)
			}
		}
	}
}

// createResponseStructFromGinH creates a response struct from gin.H literal
func (d *AnonymousStructDetector) createResponseStructFromGinH(ginHLit *ast.CompositeLit, handler *HandlerInfo, jsonCall *ast.CallExpr) *AnonymousStructInfo {
	if len(ginHLit.Elts) == 0 {
		return nil
	}

	// Create a synthetic struct type based on gin.H contents
	var fields []*ast.Field

	for _, elt := range ginHLit.Elts {
		if kvExpr, ok := elt.(*ast.KeyValueExpr); ok {
			// Extract key and value from gin.H{"key": value}
			if key, ok := kvExpr.Key.(*ast.BasicLit); ok && key.Kind == token.STRING {
				// Remove quotes from the key
				jsonKey := strings.Trim(key.Value, `"`)

				// Determine field type from the value
				fieldType := d.inferTypeFromExpr(kvExpr.Value)

				field := &ast.Field{
					Names: []*ast.Ident{{Name: d.createFieldName(jsonKey)}},
					Type:  fieldType,
					Tag:   &ast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("`json:\"%s\"`", jsonKey)},
				}
				fields = append(fields, field)
			}
		}
	}

	if len(fields) == 0 {
		return nil
	}

	structType := &ast.StructType{
		Fields: &ast.FieldList{List: fields},
	}

	return &AnonymousStructInfo{
		VariableName:   "response",
		HandlerName:    handler.Name,
		BindingType:    BindingTypeResponse,
		StructType:     structType,
		Position:       ginHLit.Pos(),
		ResponseCall:   jsonCall,
		IsResponse:     true,
		HTTPStatusCode: d.extractHTTPStatusCode(jsonCall),
	}
}

// inferTypeFromExpr infers Go type from expression
func (d *AnonymousStructDetector) inferTypeFromExpr(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return &ast.Ident{Name: "string"}
		} else if e.Kind == token.INT {
			return &ast.Ident{Name: "int"}
		} else if e.Kind == token.FLOAT {
			return &ast.Ident{Name: "float64"}
		} else if e.Kind == token.CHAR {
			return &ast.Ident{Name: "rune"}
		}
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return &ast.Ident{Name: "bool"}
		}
		// For other identifiers, use their type directly
		return &ast.Ident{Name: "interface{}"}
	case *ast.CompositeLit:
		// For composite literals, use interface{}
		return &ast.Ident{Name: "interface{}"}
	}

	return &ast.Ident{Name: "interface{}"}
}

// toCamelCase converts snake_case to camelCase
func (d *AnonymousStructDetector) toCamelCase(s string) string {
	words := strings.Split(s, "_")
	for i, word := range words {
		if i > 0 && len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
		}
	}
	return strings.Join(words, "")
}

// createFieldName creates a proper Go field name from a JSON key
func (d *AnonymousStructDetector) createFieldName(jsonKey string) string {
	if jsonKey == "" {
		return "Field"
	}

	// Remove any remaining quotes that might not have been stripped
	jsonKey = strings.Trim(jsonKey, "\"")

	// For single character keys, capitalize them
	if len(jsonKey) == 1 {
		return strings.ToUpper(jsonKey)
	}

	// For special cases like "id" -> "ID" (all uppercase 2-3 letter acronyms)
	specialCases := map[string]string{
		"id":    "ID",
		"uuid":  "UUID",
		"url":   "URL",
		"api":   "API",
		"http":  "HTTP",
		"https": "HTTPS",
		"json":  "JSON",
		"xml":   "XML",
		"sql":   "SQL",
		"db":    "DB",
	}
	if upperCase, exists := specialCases[strings.ToLower(jsonKey)]; exists {
		return upperCase
	}

	// For keys like "ID" (already uppercase), keep as is
	if strings.ToUpper(jsonKey) == jsonKey && len(jsonKey) <= 4 {
		return jsonKey
	}

	// For camelCase like "userName", convert to "UserName" (capitalize first letter)
	if unicode.IsLower(rune(jsonKey[0])) {
		return strings.ToUpper(jsonKey[:1]) + jsonKey[1:]
	}

	// For PascalCase like "UserName", keep as is
	if unicode.IsUpper(rune(jsonKey[0])) {
		return jsonKey
	}

	// For snake_case like "user_name", use toCamelCase and capitalize first letter
	camelCase := d.toCamelCase(jsonKey)
	if len(camelCase) > 0 {
		return strings.ToUpper(camelCase[:1]) + camelCase[1:]
	}

	// Default: capitalize first letter
	if len(jsonKey) > 0 {
		return strings.ToUpper(jsonKey[:1]) + jsonKey[1:]
	}

	return jsonKey
}

// extractDocComment extracts the documentation comment for a function
func (d *AnonymousStructDetector) extractDocComment(funcDecl *ast.FuncDecl) string {
	if funcDecl.Doc == nil {
		return ""
	}

	var comments []string
	seenComments := make(map[string]bool) // Track seen comments to deduplicate

	for _, comment := range funcDecl.Doc.List {
		// Remove the "// " prefix from each comment line
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))

		// Only add if we haven't seen this comment before
		if !seenComments[text] {
			comments = append(comments, text)
			seenComments[text] = true
		}
	}

	return strings.Join(comments, "\n")
}

// extractHTTPStatusCode extracts the HTTP status code from a c.JSON call
func (d *AnonymousStructDetector) extractHTTPStatusCode(jsonCall *ast.CallExpr) int {
	if len(jsonCall.Args) < 1 {
		return 200 // Default to 200 if no status code found
	}

	// First argument should be the status code
	statusExpr := jsonCall.Args[0]

	// Try to extract integer literal
	if basicLit, ok := statusExpr.(*ast.BasicLit); ok && basicLit.Kind == token.INT {
		// Parse the integer
		var statusCode int
		_, err := fmt.Sscanf(basicLit.Value, "%d", &statusCode)
		if err == nil {
			return statusCode
		}
	}

	// Try to extract identifiers (like http.StatusOK, http.StatusCreated)
	if ident, ok := statusExpr.(*ast.Ident); ok {
		switch ident.Name {
		case "StatusOK":
			return 200
		case "StatusCreated":
			return 201
		case "StatusAccepted":
			return 202
		case "StatusNoContent":
			return 204
		case "StatusBadRequest":
			return 400
		case "StatusUnauthorized":
			return 401
		case "StatusForbidden":
			return 403
		case "StatusNotFound":
			return 404
		case "StatusInternalServerError":
			return 500
		}
	}

	return 200 // Default fallback
}

// inferRouteInfo attempts to infer route information from handler name and context
func (d *AnonymousStructDetector) inferRouteInfo(handlerName string) *router.RouteInfo {
	// Basic route inference based on handler name patterns
	method := "GET" // Default method
	path := "/" + strings.ToLower(handlerName)

	// Infer HTTP method from handler name patterns
	if strings.Contains(strings.ToLower(handlerName), "create") {
		method = "POST"
		path = "/" + strings.ToLower(strings.TrimPrefix(handlerName, "Create"))
	} else if strings.Contains(strings.ToLower(handlerName), "update") {
		method = "PUT"
		path = "/" + strings.ToLower(strings.TrimPrefix(handlerName, "Update")) + "/:id"
	} else if strings.Contains(strings.ToLower(handlerName), "delete") {
		method = "DELETE"
		path = "/" + strings.ToLower(strings.TrimPrefix(handlerName, "Delete")) + "/:id"
	} else if strings.Contains(strings.ToLower(handlerName), "show") {
		path = "/" + strings.ToLower(strings.TrimPrefix(handlerName, "Show")) + "/:id"
	} else if strings.Contains(strings.ToLower(handlerName), "index") {
		path = "/" + strings.ToLower(strings.TrimPrefix(handlerName, "Index"))
	}

	return &router.RouteInfo{
		Method:     method,
		Path:       path,
		IsInferred: true,
	}
}

// addRouteInformation adds route information to all handlers
func (d *AnonymousStructDetector) addRouteInformation() {
	for _, handler := range d.handlers {
		handler.Route = d.inferRouteInfo(handler.Name)
	}
}

// addRouteInformationFromFile adds route information by parsing a router file
func (d *AnonymousStructDetector) addRouteInformationFromFile(routerFilePath, stripRoutePrefix string) error {
	// Convert handlers map to slice of interface type
	var handlerSlice []router.HandlerInfo
	for _, handler := range d.handlers {
		handlerSlice = append(handlerSlice, handler)
	}

	// Parse router file and update handlers with actual route information
	err := router.ParseRouterAndAnalyzeHandlers(routerFilePath, handlerSlice, stripRoutePrefix)
	if err != nil {
		return fmt.Errorf("failed to parse router file: %w", err)
	}

	// Update handlers in the map with route information
	for _, handler := range handlerSlice {
		if originalHandler, exists := d.handlers[handler.GetName()]; exists {
			originalHandler.Route = handler.GetRoute()
			d.handlers[handler.GetName()] = originalHandler
		}
	}

	return nil
}

// FindAnonymousStructsInFile is a convenience function that analyzes a file
func FindAnonymousStructsInFile(fileSet *token.FileSet, file *ast.File, pkgName string) (FileAnalysisResult, error) {
	detector := NewAnonymousStructDetector(fileSet, file, pkgName)
	result, err := detector.DetectAnonymousStructsWithRouter("", "")
	if err != nil {
		return result, err
	}

	// Validate that we found something
	if len(result.AnonymousStructs) == 0 {
		return result, fmt.Errorf("no anonymous structs found in file %s", result.FilePath)
	}

	return result, nil
}

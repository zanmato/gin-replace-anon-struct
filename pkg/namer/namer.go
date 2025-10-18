package namer

import (
	"fmt"
	"strings"
	"unicode"

	"gin-replace-anon-struct/pkg/analyzer"
)

// TypeNameGenerator generates appropriate type names for anonymous structs
type TypeNameGenerator struct {
	// Used type names to avoid conflicts
	usedNames map[string]bool
}

// NewTypeNameGenerator creates a new type name generator
func NewTypeNameGenerator() *TypeNameGenerator {
	return &TypeNameGenerator{
		usedNames: make(map[string]bool),
	}
}

// GenerateTypeName generates a type name for an anonymous struct
func (g *TypeNameGenerator) GenerateTypeName(
	handlerName string,
	variableName string,
	bindingType analyzer.BindingType,
	existingTypes []string,
) string {
	// Clean up handler name (remove package prefix if present)
	handlerName = g.cleanHandlerName(handlerName)

	// Generate base name based on binding type and context
	baseName := g.generateBaseName(handlerName, variableName, bindingType)

	// Make sure it's a valid Go type name (first letter uppercase)
	typeName := g.makeValidTypeName(baseName)

	// Handle conflicts by adding suffixes
	finalName := g.resolveConflict(typeName, existingTypes)

	// Mark as used
	g.usedNames[finalName] = true

	return finalName
}

// cleanHandlerName removes package prefixes and cleans up the handler name
func (g *TypeNameGenerator) cleanHandlerName(handlerName string) string {
	// Remove package prefix if present
	if dotIndex := strings.LastIndex(handlerName, "."); dotIndex != -1 {
		handlerName = handlerName[dotIndex+1:]
	}

	// Remove common suffixes
	handlerName = strings.TrimSuffix(handlerName, "_inner")
	handlerName = strings.TrimSuffix(handlerName, "Handler")
	handlerName = strings.TrimSuffix(handlerName, "Func")

	return handlerName
}

// generateBaseName generates the base type name based on context
func (g *TypeNameGenerator) generateBaseName(handlerName, variableName string, bindingType analyzer.BindingType) string {
	// Variable name heuristics
	if g.isQueryParamVarName(variableName) {
		// Common query param variable names
		return handlerName + "QueryParams"
	}

	if g.isRequestBodyVarName(variableName) {
		// Common request body variable names - include variable name for uniqueness
		//	return handlerName + g.capitalize(variableName) + "Request"
		return handlerName + "Request"
	}

	// Response binding type - always use handler name for responses
	if bindingType == analyzer.BindingTypeResponse {
		return handlerName + "Response"
	}

	// Fallback: use binding type to determine suffix with variable name for uniqueness
	switch bindingType {
	case analyzer.BindingTypeQuery:
		return handlerName + "QueryParams"
	case analyzer.BindingTypeJSON:
		return handlerName + "Request"
	case analyzer.BindingTypeForm:
		return handlerName + "FormData"
	case analyzer.BindingTypeBind:
		return handlerName + "BindingData"
	default:
		return handlerName + g.capitalize(variableName) + "Data"
	}
}

// isQueryParamVarName checks if the variable name suggests it's used for query parameters
func (g *TypeNameGenerator) isQueryParamVarName(name string) bool {
	commonQueryParamNames := map[string]bool{
		"q":       true,
		"query":   true,
		"params":  true,
		"filters": true,
		"opts":    true,
		"options": true,
		"search":  true,
		"filter":  true,
	}

	// Single letter names that are commonly used for query params
	if len(name) == 1 {
		lower := strings.ToLower(name)
		return lower == "q" || lower == "f" || lower == "p"
	}

	return commonQueryParamNames[strings.ToLower(name)]
}

// isRequestBodyVarName checks if the variable name suggests it's used for request body
func (g *TypeNameGenerator) isRequestBodyVarName(name string) bool {
	commonRequestBodyNames := map[string]bool{
		"req":     true,
		"request": true,
		"body":    true,
		"data":    true,
		"input":   true,
		"payload": true,
	}

	// Single letter names that are commonly used for request body
	if len(name) == 1 {
		lower := strings.ToLower(name)
		return lower == "r" || lower == "b" || lower == "d"
	}

	return commonRequestBodyNames[strings.ToLower(name)]
}

// makeValidTypeName ensures the name is a valid Go type name
func (g *TypeNameGenerator) makeValidTypeName(name string) string {
	if name == "" {
		return "AnonymousStruct"
	}

	// Convert to proper camelCase
	name = g.toCamelCase(name)

	// Ensure first character is uppercase (for exported types)
	if len(name) > 0 && unicode.IsLower(rune(name[0])) {
		name = string(unicode.ToUpper(rune(name[0]))) + name[1:]
	}

	// Remove any non-alphanumeric characters
	var cleanName strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cleanName.WriteRune(r)
		}
	}

	result := cleanName.String()
	if result == "" {
		return "AnonymousStruct"
	}

	return result
}

// capitalize capitalizes the first letter of a string
func (g *TypeNameGenerator) capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(string(s[0])) + s[1:]
}

// toCamelCase converts a string to proper camelCase
func (g *TypeNameGenerator) toCamelCase(name string) string {
	// Handle common suffixes by converting them to proper format
	if strings.HasSuffix(name, "QueryParams") {
		return strings.TrimSuffix(name, "QueryParams") + "QueryParams"
	} else if strings.HasSuffix(name, "Request") {
		return strings.TrimSuffix(name, "Request") + "Request"
	} else if strings.HasSuffix(name, "Response") {
		return strings.TrimSuffix(name, "Response") + "Response"
	} else if strings.HasSuffix(name, "FormData") {
		return strings.TrimSuffix(name, "FormData") + "FormData"
	} else if strings.HasSuffix(name, "BindingData") {
		return strings.TrimSuffix(name, "BindingData") + "BindingData"
	} else if strings.HasSuffix(name, "Data") {
		return strings.TrimSuffix(name, "Data") + "Data"
	}

	// For other cases, just return as is
	return name
}

// resolveConflict handles naming conflicts by adding numeric suffixes
func (g *TypeNameGenerator) resolveConflict(baseName string, existingTypes []string) string {
	if !g.isNameUsed(baseName, existingTypes) {
		return baseName
	}

	// Try adding numeric suffixes
	for i := 1; i < 1000; i++ {
		candidateName := fmt.Sprintf("%s%d", baseName, i)
		if !g.isNameUsed(candidateName, existingTypes) {
			return candidateName
		}
	}

	// Fallback: use a random name
	return fmt.Sprintf("%s_%x", baseName, len(existingTypes))
}

// isNameUsed checks if a name is already used
func (g *TypeNameGenerator) isNameUsed(name string, existingTypes []string) bool {
	// Check our internal used names
	if g.usedNames[name] {
		return true
	}

	// Check existing types
	for _, existing := range existingTypes {
		if existing == name {
			return true
		}
	}

	return false
}

// GenerateAllTypeNames generates type names for all anonymous structs in a file
func GenerateAllTypeNames(result analyzer.FileAnalysisResult) map[string]string {
	generator := NewTypeNameGenerator()
	nameMap := make(map[string]string)

	// Collect existing type names to avoid conflicts
	var existingTypes []string
	for _, handler := range result.Handlers {
		existingTypes = append(existingTypes, handler.Name)
	}

	// Generate names for each anonymous struct
	for _, anonStruct := range result.AnonymousStructs {
		key := fmt.Sprintf("%s:%s:%s", anonStruct.HandlerName, anonStruct.VariableName, anonStruct.BindingType)
		typeName := generator.GenerateTypeName(
			anonStruct.HandlerName,
			anonStruct.VariableName,
			anonStruct.BindingType,
			existingTypes,
		)
		nameMap[key] = typeName
		existingTypes = append(existingTypes, typeName)
	}

	return nameMap
}

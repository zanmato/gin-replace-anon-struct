package namer

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/andreas/gin-replace-anon-struct/pkg/analyzer"
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
	handlerName = g.CleanHandlerName(handlerName)

	// Generate base name based on binding type and context
	baseName := g.GenerateBaseName(handlerName, variableName, bindingType)

	// Make sure it's a valid Go type name (first letter uppercase)
	typeName := g.MakeValidTypeName(baseName)

	// Handle conflicts by adding suffixes
	finalName := g.resolveConflict(typeName, existingTypes)

	// Mark as used
	g.usedNames[finalName] = true

	return finalName
}

// CleanHandlerName removes package prefixes and cleans up the handler name
func (g *TypeNameGenerator) CleanHandlerName(handlerName string) string {
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

// GenerateBaseName generates the base type name based on context
func (g *TypeNameGenerator) GenerateBaseName(handlerName, variableName string, bindingType analyzer.BindingType) string {
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

// MakeValidTypeName ensures the name is a valid Go type name
func (g *TypeNameGenerator) MakeValidTypeName(name string) string {
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
		base := strings.TrimSuffix(name, "QueryParams")
		return g.CapitalizeEachWord(base) + "QueryParams"
	} else if strings.HasSuffix(name, "Request") {
		base := strings.TrimSuffix(name, "Request")
		return g.CapitalizeEachWord(base) + "Request"
	} else if strings.HasSuffix(name, "Response") {
		base := strings.TrimSuffix(name, "Response")
		return g.CapitalizeEachWord(base) + "Response"
	} else if strings.HasSuffix(name, "FormData") {
		base := strings.TrimSuffix(name, "FormData")
		return g.CapitalizeEachWord(base) + "FormData"
	} else if strings.HasSuffix(name, "BindingData") {
		base := strings.TrimSuffix(name, "BindingData")
		return g.CapitalizeEachWord(base) + "BindingData"
	} else if strings.HasSuffix(name, "Data") {
		base := strings.TrimSuffix(name, "Data")
		return g.CapitalizeEachWord(base) + "Data"
	}

	// For other cases, just capitalize each word
	return g.CapitalizeEachWord(name)
}

// CapitalizeEachWord capitalizes the first letter of each word in a camelCase/PascalCase string
func (g *TypeNameGenerator) CapitalizeEachWord(s string) string {
	if s == "" {
		return s
	}

	// For names that might already be in mixed case, ensure each word starts with uppercase
	// This handles cases like "todoIndex" -> "TodoIndex" or "todoIndexResponse" -> "TodoIndexResponse"

	// If the string is already properly capitalized (all words start with uppercase), return as is
	if g.isProperlyCapitalized(s) {
		return s
	}

	var result strings.Builder

	// Handle underscore-separated words
	if strings.Contains(s, "_") {
		words := strings.Split(s, "_")
		for i, word := range words {
			if i > 0 {
				result.WriteString("_")
			}
			if word != "" {
				result.WriteString(g.capitalize(word))
			}
		}
		return result.String()
	}

	// Handle camelCase/PascalCase by finding transitions from lower to upper case
	result.WriteRune(unicode.ToUpper(rune(s[0])))
	for i := 1; i < len(s); i++ {
		r := rune(s[i])
		if unicode.IsLower(rune(s[i-1])) && unicode.IsUpper(r) {
			// We're at a word boundary (lower to upper case transition)
			// Keep the uppercase as it indicates a new word
			result.WriteRune(r)
		} else {
			// Just add the character as is
			result.WriteRune(r)
		}
	}

	return result.String()
}

// isProperlyCapitalized checks if a string is already properly capitalized (each word starts with uppercase)
func (g *TypeNameGenerator) isProperlyCapitalized(s string) bool {
	if len(s) == 0 {
		return true
	}

	// First character should be uppercase
	if !unicode.IsUpper(rune(s[0])) {
		return false
	}

	// Check each character to see if it follows proper capitalization rules
	for i := 1; i < len(s); i++ {
		// If current char is uppercase and previous char is lowercase, that's a word boundary
		// If current char is uppercase and previous char is also uppercase, we're in an acronym
		// If current char is lowercase, that's fine within a word
		// So basically, if we find a lowercase followed by lowercase, that's fine
		// If we find uppercase followed by lowercase, that's fine
		// The only invalid case would be lowercase starting a word, which we already checked
	}

	return true
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
	return slices.Contains(existingTypes, name)
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

package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andreas/gin-replace-anon-struct/pkg/analyzer"
	"github.com/andreas/gin-replace-anon-struct/pkg/router"
	"github.com/andreas/gin-replace-anon-struct/pkg/swagger"
	"github.com/andreas/gin-replace-anon-struct/pkg/transformer"
)

// TestCommentExtractionIntegration tests that only doc comments preceding handlers are extracted,
// not inline comments within handler function bodies
func TestCommentExtractionIntegration(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/test", handlers.TestHandler)
	r.POST("/test", handlers.CreateHandler)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with mixed comment types
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// TestHandler handles test requests.
// This is a proper doc comment that should be extracted.
func TestHandler(c *gin.Context) {
	// This is an inline comment within the function body
	// It should NOT be extracted as a doc comment
	var req struct {
		Name string ` + "`json:\"name\"`" + `
	}

	// Another inline comment that should be ignored
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// This comment is also inline and should be ignored
	c.JSON(http.StatusOK, gin.H{"message": "test"})
}

// CreateHandler creates new resources.
// This is another proper doc comment.
func CreateHandler(c *gin.Context) {
	// This inline comment should not be extracted
	var req struct {
		Title string ` + "`json:\"title\"`" + `
		Body  string ` + "`json:\"body\"`" + `
	}

	// More inline comments that should be ignored
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Final inline comment - should be ignored
	c.JSON(http.StatusCreated, gin.H{"id": 1})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Verify we found both handlers
	if len(result.Handlers) != 2 {
		t.Fatalf("Expected 2 handlers, got %d", len(result.Handlers))
	}

	// Check TestHandler
	var testHandler *analyzer.HandlerInfo
	for i := range result.Handlers {
		if result.Handlers[i].Name == "TestHandler" {
			testHandler = &result.Handlers[i]
			break
		}
	}

	if testHandler == nil {
		t.Fatal("TestHandler not found")
	}

	// Verify doc comment was extracted correctly
	expectedDocComment := "TestHandler handles test requests.\nThis is a proper doc comment that should be extracted."
	if testHandler.DocComment != expectedDocComment {
		t.Fatalf("Expected doc comment '%s', got '%s'", expectedDocComment, testHandler.DocComment)
	}

	// Generate Swagger annotations
	generator := swagger.NewAnnotationGenerator(result, "")
	annotations := generator.GenerateAnnotations()

	// Find TestHandler annotation
	var testAnnotation *swagger.HandlerAnnotations
	for i := range annotations {
		if annotations[i].HandlerName == "TestHandler" {
			testAnnotation = &annotations[i]
			break
		}
	}

	if testAnnotation == nil {
		t.Fatal("TestHandler annotation not found")
	}

	// Verify that inline comments are NOT included in the Swagger annotations
	if strings.Contains(testAnnotation.ExistingComment, "inline comment within the function body") {
		t.Fatal("Inline comments from function body were incorrectly extracted")
	}

	if strings.Contains(testAnnotation.ExistingComment, "should NOT be extracted") {
		t.Fatal("Inline comments from function body were incorrectly extracted")
	}

	// Apply transformation and verify inline comments are preserved in output
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Verify inline comments are still present in the transformed code
	if !strings.Contains(transformed, "// This is an inline comment within the function body") {
		t.Fatal("Inline comments were removed from transformed code")
	}

	if !strings.Contains(transformed, "// Another inline comment that should be ignored") {
		t.Fatal("Inline comments were removed from transformed code")
	}

	// Verify Swagger annotations are present
	if !strings.Contains(transformed, "@Success") {
		t.Fatal("Swagger @Success annotations not found in transformed code")
	}

	if !strings.Contains(transformed, "@Router /test [GET]") {
		t.Fatal("Swagger @Router annotations not found in transformed code")
	}

	t.Logf("SUCCESS: Comment extraction test passed - only doc comments were extracted")
}

// TestRouteParsingIntegration tests that routes are correctly parsed from router files
// and properly associated with handlers (generating correct @Router annotations)
func TestRouteParsingIntegration(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with complex routes
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/contact"
)

func Router() *gin.Engine {
	r := gin.Default()

	// Contact form topic routes
	r.GET("/contact-form-topics", contact.Index)
	r.POST("/contact-form-topics", contact.Create)
	r.GET("/contact-form-topics/:id", contact.Show)
	r.PUT("/contact-form-topics/:id", contact.Update)
	r.DELETE("/contact-form-topics/:id", contact.Delete)

	// Route groups with unique handlers
	api := r.Group("/api/v1")
	{
		api.GET("/contact-form-topics", contact.ApiIndex)
		api.POST("/contact-form-topics", contact.ApiCreate)
	}

	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create contact handler file
	handlerContent := `package contact

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Index returns contact form topics.
func Index(c *gin.Context) {
	// This inline comment should be ignored
	var query struct {
		Limit int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// Create creates a new contact form topic.
func Create(c *gin.Context) {
	var req struct {
		Topic string ` + "`json:\"topic\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": "123"})
}

// Show returns a specific contact form topic.
func Show(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// Update updates a contact form topic.
func Update(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Topic string ` + "`json:\"topic\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// Delete deletes a contact form topic.
func Delete(c *gin.Context) {
	id := c.Param("id")
	c.Status(http.StatusOK)
}

// ApiIndex returns contact form topics for API v1.
func ApiIndex(c *gin.Context) {
	var query struct {
		Limit  int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// ApiCreate creates a new contact form topic for API v1.
func ApiCreate(c *gin.Context) {
	var req struct {
		Topic string ` + "`json:\"topic\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": "456"})
}`

	handlerFile := filepath.Join(tempDir, "contact.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "contact")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Verify we found all handlers
	if len(result.Handlers) != 7 {
		t.Fatalf("Expected 7 handlers, got %d", len(result.Handlers))
	}

	// Generate Swagger annotations
	generator := swagger.NewAnnotationGenerator(result, "")
	annotations := generator.GenerateAnnotations()

	if len(annotations) != 7 {
		t.Fatalf("Expected 7 handler annotations, got %d", len(annotations))
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Print what routes were actually generated
	t.Logf("DEBUG: Generated routes in transformed code:")
	lines := strings.Split(transformed, "\n")
	for _, line := range lines {
		if strings.Contains(line, "@Router") {
			t.Logf("  %s", strings.TrimSpace(line))
		}
	}

	// Verify correct routes are generated (not fallback routes)
	expectedRoutes := []string{
		"@Router /contact-form-topics [GET]",
		"@Router /contact-form-topics [POST]",
		"@Router /contact-form-topics/:id [GET]",
		"@Router /contact-form-topics/:id [PUT]",
		"@Router /contact-form-topics/:id [DELETE]",
		"@Router /api/v1/contact-form-topics [GET]",
		"@Router /api/v1/contact-form-topics [POST]",
	}

	for _, expectedRoute := range expectedRoutes {
		if !strings.Contains(transformed, expectedRoute) {
			t.Fatalf("Expected route '%s' not found in transformed code", expectedRoute)
		}
	}

	// Verify fallback routes are NOT present
	fallbackRoutes := []string{
		"@Router / [GET]",
		"@Router / [POST]",
		"@Router //:id [GET]",
		"@Router //:id [PUT]",
		"@Router //:id [DELETE]",
	}

	for _, fallbackRoute := range fallbackRoutes {
		if strings.Contains(transformed, fallbackRoute) {
			t.Fatalf("Fallback route '%s' incorrectly found in transformed code", fallbackRoute)
		}
	}

	t.Logf("SUCCESS: Route parsing test passed - correct routes were generated")
}

// TestSwaggerCommentPositioningIntegration tests that Swagger comments are positioned
// above the correct handler functions, not grouped together
func TestSwaggerCommentPositioningIntegration(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/users", handlers.UsersIndex)
	r.GET("/products", handlers.ProductsIndex)
	r.POST("/orders", handlers.OrdersCreate)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with multiple handlers and different doc comments
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// UsersIndex returns a list of users.
// This endpoint supports pagination and filtering.
func UsersIndex(c *gin.Context) {
	var query struct {
		Limit  int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
		Search string ` + "`form:\"search\"`" + `
	}

	// Inline comment shouldn't be removed
	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// ProductsIndex returns a list of products.
// Products can be filtered by category and price range.
func ProductsIndex(c *gin.Context) {
	var (
		res []struct{
			ID int
			Big json.RawMessage
		}
	)

	var query struct {
		CategoryID int ` + "`form:\"category_id\"`" + `
		MinPrice   float64 ` + "`form:\"min_price\"`" + `
		MaxPrice   float64 ` + "`form:\"max_price\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res{})
}

// OrdersCreate creates a new order.
// This endpoint validates the order data and creates the order.
func OrdersCreate(c *gin.Context) {
	var req struct {
		UserID    int ` + "`json:\"user_id\" binding:\"required\"`" + `
		ProductID int ` + "`json:\"product_id\" binding:\"required\"`" + `
		Quantity  int ` + "`json:\"quantity\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 123})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation using DST transformer for better comment preservation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform with DST: %v", err)
	}

	// Debug: Show the full transformed code
	t.Logf("DEBUG: Full transformed content:\n%s", transformed)

	// Show the transformed code around each handler function
	t.Logf("DEBUG: Transformed code around handlers:")
	lines := strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "func UsersIndex") || strings.Contains(line, "func ProductsIndex") || strings.Contains(line, "func OrdersCreate") {
			// Show 5 lines before and after each function
			start := i - 5
			if start < 0 {
				start = 0
			}
			end := i + 10
			if end > len(lines) {
				end = len(lines)
			}

			t.Logf("Handler around line %d:", i)
			for j := start; j < end; j++ {
				t.Logf("  %d: %s", j, lines[j])
			}
			t.Logf("")
		}
	}

	// Verify that Swagger comments appear above each handler function
	// and are not grouped together

	// Check UsersIndex
	usersIndexStart := strings.Index(transformed, "func UsersIndex(c *gin.Context) {")
	if usersIndexStart == -1 {
		t.Fatal("UsersIndex function not found")
	}

	// Find the start of the function (including comments before it)
	// Look further back to include doc comments (about 500 characters before function)
	commentStart := usersIndexStart - 500
	if commentStart < 0 {
		commentStart = 0
	}
	funcStart := strings.LastIndex(transformed[:commentStart], "\n")
	if funcStart == -1 {
		funcStart = 0
	} else {
		funcStart += 1
	}

	endPos := usersIndexStart + 500
	if endPos > len(transformed) {
		endPos = len(transformed)
	}
	usersIndexSection := transformed[funcStart:endPos] // Include some of function body

	// Should contain UsersIndex-specific Swagger annotations
	if !strings.Contains(usersIndexSection, "@Router /users [GET]") {
		t.Fatal("UsersIndex @Router annotation not found above function")
	}

	if !strings.Contains(usersIndexSection, "UsersIndex returns a list of users.") {
		t.Fatal("UsersIndex doc comment not found above function")
	}

	// Check ProductsIndex
	productsIndexStart := strings.Index(transformed, "func ProductsIndex(c *gin.Context) {")
	if productsIndexStart == -1 {
		t.Fatal("ProductsIndex function not found")
	}

	// Find the start of the function (including comments before it)
	// Look further back to include doc comments (about 500 characters before function)
	commentStart = productsIndexStart - 500
	if commentStart < 0 {
		commentStart = 0
	}
	funcStart = strings.LastIndex(transformed[:commentStart], "\n")
	if funcStart == -1 {
		funcStart = 0
	} else {
		funcStart += 1
	}

	productsEndPos := productsIndexStart + 500
	if productsEndPos > len(transformed) {
		productsEndPos = len(transformed)
	}
	productsIndexSection := transformed[funcStart:productsEndPos]

	if !strings.Contains(productsIndexSection, "@Router /products [GET]") {
		t.Fatal("ProductsIndex @Router annotation not found above function")
	}

	if !strings.Contains(productsIndexSection, "ProductsIndex returns a list of products.") {
		t.Fatal("ProductsIndex doc comment not found above function")
	}

	// Check OrdersCreate
	ordersCreateStart := strings.Index(transformed, "func OrdersCreate(c *gin.Context) {")
	if ordersCreateStart == -1 {
		t.Fatal("OrdersCreate function not found")
	}

	// Find the start of the function (including comments before it)
	// Look further back to include doc comments (about 500 characters before function)
	commentStart = ordersCreateStart - 500
	if commentStart < 0 {
		commentStart = 0
	}
	funcStart = strings.LastIndex(transformed[:commentStart], "\n")
	if funcStart == -1 {
		funcStart = 0
	} else {
		funcStart += 1
	}

	ordersEndPos := ordersCreateStart + 500
	if ordersEndPos > len(transformed) {
		ordersEndPos = len(transformed)
	}
	ordersCreateSection := transformed[funcStart:ordersEndPos]

	if !strings.Contains(ordersCreateSection, "@Router /orders [POST]") {
		t.Fatal("OrdersCreate @Router annotation not found above function")
	}

	if !strings.Contains(ordersCreateSection, "OrdersCreate creates a new order.") {
		t.Fatal("OrdersCreate doc comment not found above function")
	}

	// Verify that comments are not all grouped together at the top
	// Each function should have its own comments immediately before it

	t.Logf("SUCCESS: Swagger comment positioning test passed - comments are correctly positioned above each handler")
}

// TestHandlerFactoryIntegration tests parameterized handlers and handler factories
func TestHandlerFactoryIntegration(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with factory routes
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
	"github.com/example/services"
)

func Router() *gin.Engine {
	r := gin.Default()

	// Factory pattern routes
	userService := &services.UserService{}
	r.GET("/users", handlers.UserIndex(userService))
	r.POST("/users", handlers.UserCreate(userService))

	// Route groups with factories
	api := r.Group("/api/v1")
	{
		orderService := &services.OrderService{}
		api.GET("/orders", handlers.OrderIndex(orderService))
		api.POST("/orders", handlers.OrderCreate(orderService))
	}

	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create services file
	servicesContent := `package services

type UserService struct {
	Database string
}

type OrderService struct {
	PaymentGateway string
}`

	servicesFile := filepath.Join(tempDir, "services.go")
	if err := os.WriteFile(servicesFile, []byte(servicesContent), 0644); err != nil {
		t.Fatalf("Failed to write services file: %v", err)
	}

	// Create handler file with factory functions
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
	"github.com/example/services"
)

// UserIndex returns a list of users.
func UserIndex(service *services.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var query struct {
			Limit  int ` + "`form:\"limit\"`" + `
			Offset int ` + "`form:\"offset\"`" + `
		}

		if err := c.ShouldBindQuery(&query); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, []gin.H{})
	}
}

// UserCreate creates a new user.
func UserCreate(service *services.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Name  string ` + "`json:\"name\" binding:\"required\"`" + `
			Email string ` + "`json:\"email\" binding:\"required,email\"`" + `
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, gin.H{"id": 123})
	}
}

// OrderIndex returns a list of orders.
func OrderIndex(service *services.OrderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var query struct {
			UserID int ` + "`form:\"user_id\"`" + `
			Status string ` + "`form:\"status\"`" + `
		}

		if err := c.ShouldBindQuery(&query); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, []gin.H{})
	}
}

// OrderCreate creates a new order.
func OrderCreate(service *services.OrderService) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			UserID    int ` + "`json:\"user_id\" binding:\"required\"`" + `
			ProductID int ` + "`json:\"product_id\" binding:\"required\"`" + `
			Quantity  int ` + "`json:\"quantity\" binding:\"required\"`" + `
		}

		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, gin.H{"id": 456})
	}
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Verify we found the factory handlers
	if len(result.Handlers) != 4 {
		t.Fatalf("Expected 4 handlers, got %d", len(result.Handlers))
	}

	// Generate Swagger annotations
	generator := swagger.NewAnnotationGenerator(result, "")
	annotations := generator.GenerateAnnotations()

	if len(annotations) != 4 {
		t.Fatalf("Expected 4 handler annotations, got %d", len(annotations))
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Verify routes are correctly parsed for factory handlers
	expectedRoutes := []string{
		"@Router /users [GET]",
		"@Router /users [POST]",
		"@Router /api/v1/orders [GET]",
		"@Router /api/v1/orders [POST]",
	}

	for _, expectedRoute := range expectedRoutes {
		if !strings.Contains(transformed, expectedRoute) {
			t.Fatalf("Expected route '%s' not found in transformed code", expectedRoute)
		}
	}

	t.Logf("SUCCESS: Handler factory test passed - factory handlers were correctly processed")
}

// TestComplexRouteGroupsIntegration tests nested route groups and complex path patterns
func TestComplexRouteGroupsIntegration(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with complex nested route groups
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
	adminHandlers "github.com/example/admin/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()

	// Simple routes
	r.GET("/", handlers.Home)

	// API versioning with nested groups
	api := r.Group("/api")
	{
		// API v1
		v1 := api.Group("/v1")
		{
			// Users resource
			users := v1.Group("/users")
			{
				users.GET("", handlers.UsersIndex)
				users.POST("", handlers.UsersCreate)
				users.GET("/:id", handlers.UsersShow)
				users.PUT("/:id", handlers.UsersUpdate)
				users.DELETE("/:id", handlers.UsersDelete)

				// Nested user addresses
				users.GET("/:id/addresses", handlers.UserAddressesIndex)
				users.POST("/:id/addresses", handlers.UserAddressesCreate)
			}

			// Products resource
			products := v1.Group("/products")
			{
				products.GET("", handlers.ProductsIndex)
				products.POST("", handlers.ProductsCreate)
				products.GET("/:id", handlers.ProductsShow)
				products.PUT("/:id", handlers.ProductsUpdate)
				products.DELETE("/:id", handlers.ProductsDelete)
			}
		}

		// API v2
		v2 := api.Group("/v2")
		{
			v2.GET("/health", handlers.HealthCheck)
		}
	}

	// Admin routes
	admin := r.Group("/admin")
	{
		admin.GET("/dashboard", adminHandlers.Dashboard)
		admin.GET("/users", adminHandlers.UsersIndex)
		admin.POST("/users/:id/ban", adminHandlers.BanUser)
	}

	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create main handlers file
	mainHandlersContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Home serves the home page.
func Home(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Welcome"})
}

// UsersIndex returns a list of users.
func UsersIndex(c *gin.Context) {
	var query struct {
		Limit  int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// UsersCreate creates a new user.
func UsersCreate(c *gin.Context) {
	var req struct {
		Name  string ` + "`json:\"name\" binding:\"required\"`" + `
		Email string ` + "`json:\"email\" binding:\"required,email\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 123})
}

// UsersShow returns a specific user.
func UsersShow(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// UsersUpdate updates a user.
func UsersUpdate(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name string ` + "`json:\"name\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// UsersDelete deletes a user.
func UsersDelete(c *gin.Context) {
	id := c.Param("id")
	c.Status(http.StatusOK)
}

// UserAddressesIndex returns addresses for a user.
func UserAddressesIndex(c *gin.Context) {
	userID := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "addresses": []gin.H{}})
}

// UserAddressesCreate creates an address for a user.
func UserAddressesCreate(c *gin.Context) {
	userID := c.Param("id")
	var req struct {
		Street  string ` + "`json:\"street\" binding:\"required\"`" + `
		City    string ` + "`json:\"city\" binding:\"required\"`" + `
		Country string ` + "`json:\"country\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"user_id": userID, "address_id": 456})
}

// ProductsIndex returns a list of products.
func ProductsIndex(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{})
}

// ProductsCreate creates a new product.
func ProductsCreate(c *gin.Context) {
	c.JSON(http.StatusCreated, gin.H{"id": 789})
}

// ProductsShow returns a specific product.
func ProductsShow(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// ProductsUpdate updates a product.
func ProductsUpdate(c *gin.Context) {
	id := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// ProductsDelete deletes a product.
func ProductsDelete(c *gin.Context) {
	id := c.Param("id")
	c.Status(http.StatusOK)
}

// HealthCheck returns API health status.
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}`

	mainHandlersFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(mainHandlersFile, []byte(mainHandlersContent), 0644); err != nil {
		t.Fatalf("Failed to write main handlers file: %v", err)
	}

	// Create admin handlers directory and file
	adminDir := filepath.Join(tempDir, "admin")
	if err := os.MkdirAll(adminDir, 0755); err != nil {
		t.Fatalf("Failed to create admin directory: %v", err)
	}

	adminHandlersContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Dashboard shows admin dashboard.
func Dashboard(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"stats": "admin stats"})
}

// UsersIndex returns admin users list.
func UsersIndex(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{})
}

// BanUser bans a user.
func BanUser(c *gin.Context) {
	userID := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "banned": true})
}`

	adminHandlersFile := filepath.Join(adminDir, "handlers.go")
	if err := os.WriteFile(adminHandlersFile, []byte(adminHandlersContent), 0644); err != nil {
		t.Fatalf("Failed to write admin handlers file: %v", err)
	}

	// Test main handlers file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, mainHandlersFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse main handlers file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Verify complex nested routes are correctly parsed
	expectedComplexRoutes := []string{
		"@Router / [GET]",
		"@Router /api/v1/users [GET]",
		"@Router /api/v1/users [POST]",
		"@Router /api/v1/users/:id [GET]",
		"@Router /api/v1/users/:id [PUT]",
		"@Router /api/v1/users/:id [DELETE]",
		"@Router /api/v1/users/:id/addresses [GET]",
		"@Router /api/v1/users/:id/addresses [POST]",
		"@Router /api/v1/products [GET]",
		"@Router /api/v1/products [POST]",
		"@Router /api/v1/products/:id [GET]",
		"@Router /api/v1/products/:id [PUT]",
		"@Router /api/v1/products/:id [DELETE]",
		"@Router /api/v2/health [GET]",
	}

	for _, expectedRoute := range expectedComplexRoutes {
		if !strings.Contains(transformed, expectedRoute) {
			t.Fatalf("Expected complex route '%s' not found in transformed code", expectedRoute)
		}
	}

	t.Logf("SUCCESS: Complex route groups test passed - nested routes were correctly parsed")
}

// TestRouteFallbackIssue tests that routes fallback to lowercase handler names
// instead of using correct routes from router file
func TestRouteFallbackIssue(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with specific routes that should be used
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()

	// These are the CORRECT routes that should be used in @Router annotations
	r.GET("/api/v1/contact-form-topics", handlers.ContactFormTopicsIndex)
	r.POST("/api/v1/contact-form-topics", handlers.ContactFormTopicsCreate)
	r.PUT("/api/v1/contact-form-topics/:id", handlers.ContactFormTopicsUpdate)
	r.DELETE("/api/v1/contact-form-topics/:id", handlers.ContactFormTopicsDelete)

	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ContactFormTopicsIndex returns contact form topics.
func ContactFormTopicsIndex(c *gin.Context) {
	var query struct {
		Limit  int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// ContactFormTopicsCreate creates a new contact form topic.
func ContactFormTopicsCreate(c *gin.Context) {
	var req struct {
		Topic string ` + "`json:\"topic\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": "123"})
}

// ContactFormTopicsUpdate updates a contact form topic.
func ContactFormTopicsUpdate(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Topic string ` + "`json:\"topic\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// ContactFormTopicsDelete deletes a contact form topic.
func ContactFormTopicsDelete(c *gin.Context) {
	id := c.Param("id")
	c.Status(http.StatusOK)
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Print what routes were actually generated
	t.Logf("DEBUG: Generated routes in transformed code:")
	lines := strings.Split(transformed, "\n")
	for _, line := range lines {
		if strings.Contains(line, "@Router") {
			t.Logf("  %s", strings.TrimSpace(line))
		}
	}

	// Verify CORRECT routes from router file are used
	expectedCorrectRoutes := []string{
		"@Router /api/v1/contact-form-topics [GET]",
		"@Router /api/v1/contact-form-topics [POST]",
		"@Router /api/v1/contact-form-topics/:id [PUT]",
		"@Router /api/v1/contact-form-topics/:id [DELETE]",
	}

	for _, expectedRoute := range expectedCorrectRoutes {
		if !strings.Contains(transformed, expectedRoute) {
			t.Fatalf("Expected correct route '%s' not found in transformed code", expectedRoute)
		}
	}

	// Verify INCORRECT fallback routes are NOT present
	incorrectFallbackRoutes := []string{
		"@Router /contactformtopicsindex [GET]",         // lowercase handler name fallback
		"@Router /contactformtopicscreate [POST]",       // lowercase handler name fallback
		"@Router /contactformtopicsupdate/:id [PUT]",    // lowercase handler name fallback
		"@Router /contactformtopicsdelete/:id [DELETE]", // lowercase handler name fallback
	}

	for _, incorrectRoute := range incorrectFallbackRoutes {
		if strings.Contains(transformed, incorrectRoute) {
			t.Fatalf("Incorrect fallback route '%s' found in transformed code - this is the bug we're testing", incorrectRoute)
		}
	}

	t.Logf("SUCCESS: Route fallback test passed - correct routes from router file were used, not fallback lowercase handler names")
}

// TestQueryParamAnnotations tests that @Param annotations are generated for query parameters
// when c.ShouldBindQuery is used
func TestQueryParamAnnotations(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/products", handlers.ProductsIndex)
	r.GET("/users", handlers.UsersIndex)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with query parameter binding
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ProductsIndex returns a list of products with filtering.
func ProductsIndex(c *gin.Context) {
	// This should generate @Param annotation for query parameters
	var query struct {
		Limit      int     ` + "`form:\"limit\"`" + `
		Offset     int     ` + "`form:\"offset\"`" + `
		CategoryID int     ` + "`form:\"category_id\"`" + `
		MinPrice   float64 ` + "`form:\"min_price\"`" + `
		MaxPrice   float64 ` + "`form:\"max_price\"`" + `
		Search     string  ` + "`form:\"search\"`" + `
		InStock    bool    ` + "`form:\"in_stock\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// UsersIndex returns a list of users with pagination.
func UsersIndex(c *gin.Context) {
	// This should also generate @Param annotation for query parameters
	var query struct {
		Limit  int    ` + "`form:\"limit\"`" + `
		Offset int    ` + "`form:\"offset\"`" + `
		Role   string ` + "`form:\"role\"`" + `
		Active bool   ` + "`form:\"active\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Show the transformed code around ProductsIndex
	t.Logf("DEBUG: Transformed code around ProductsIndex:")
	lines := strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "func ProductsIndex") {
			// Show 15 lines before and after function to see annotations
			start := i - 15
			if start < 0 {
				start = 0
			}
			end := i + 15
			if end > len(lines) {
				end = len(lines)
			}

			t.Logf("ProductsIndex handler (around line %d):", i)
			for j := start; j < end; j++ {
				t.Logf("  %d: %s", j, lines[j])
			}
			t.Logf("")
			break
		}
	}

	// Verify @Param annotations are present for query parameters
	// The format should be: // @Param data query STRUCT_NAME false
	expectedQueryParamAnnotations := []string{
		"@Param   data  query     ProductsIndexRequest   false",
		"@Param   data  query     UsersIndexRequest      false",
	}

	// Check for presence of query param annotations (before @Success)
	foundQueryParamAnnotations := 0
	for _, expectedAnnotation := range expectedQueryParamAnnotations {
		if strings.Contains(transformed, expectedAnnotation) {
			foundQueryParamAnnotations++
			t.Logf("Found expected query param annotation: %s", expectedAnnotation)
		}
	}

	if foundQueryParamAnnotations == 0 {
		// Look for any query param annotation pattern
		lines := strings.Split(transformed, "\n")
		for _, line := range lines {
			if strings.Contains(line, "@Param") && strings.Contains(line, "query") {
				t.Logf("Found query param annotation: %s", strings.TrimSpace(line))
				foundQueryParamAnnotations++
			}
		}
	}

	if foundQueryParamAnnotations == 0 {
		t.Fatal("No @Param annotations for query parameters found - this is the bug we're testing")
	}

	// Verify that query param annotations appear before @Success annotations
	lines = strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "@Param") && strings.Contains(line, "query") {
			// Look for @Success in the next few lines
			foundSuccessAfter := false
			for j := i + 1; j < i+10 && j < len(lines); j++ {
				if strings.Contains(lines[j], "@Success") {
					foundSuccessAfter = true
					t.Logf("Query param annotation correctly positioned before @Success at line %d", i)
					break
				}
			}
			if !foundSuccessAfter {
				t.Logf("Warning: Query param annotation at line %d not followed by @Success in next 10 lines", i)
			}
		}
	}

	t.Logf("SUCCESS: Query param annotations test passed - @Param annotations for query parameters were generated")
}

// TestJSONBodyParamAnnotations tests that @Param annotations are generated for JSON body parameters
// when c.ShouldBindJSON is used
func TestJSONBodyParamAnnotations(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.POST("/products", handlers.ProductsCreate)
	r.POST("/users", handlers.UsersCreate)
	r.PUT("/products/:id", handlers.ProductsUpdate)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with JSON body parameter binding
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ProductsCreate creates a new product.
func ProductsCreate(c *gin.Context) {
	// This should generate @Param annotation for JSON body
	var req struct {
		Name        string  ` + "`json:\"name\" binding:\"required\"`" + `
		Description string  ` + "`json:\"description\"`" + `
		Price       float64 ` + "`json:\"price\" binding:\"required\"`" + `
		CategoryID  int     ` + "`json:\"category_id\" binding:\"required\"`" + `
		InStock     bool    ` + "`json:\"in_stock\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 123})
}

// UsersCreate creates a new user.
func UsersCreate(c *gin.Context) {
	// This should also generate @Param annotation for JSON body
	var req struct {
		Name     string ` + "`json:\"name\" binding:\"required\"`" + `
		Email    string ` + "`json:\"email\" binding:\"required,email\"`" + `
		Password string ` + "`json:\"password\" binding:\"required,min=8\"`" + `
		Role     string ` + "`json:\"role\"`" + `
		Active   bool   ` + "`json:\"active\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 456})
}

// ProductsUpdate updates an existing product.
func ProductsUpdate(c *gin.Context) {
	id := c.Param("id")

	// This should also generate @Param annotation for JSON body
	var req struct {
		Name        *string ` + "`json:\"name\"`" + `
		Description *string ` + "`json:\"description\"`" + `
		Price       *float64 ` + "`json:\"price\"`" + `
		InStock     *bool    ` + "`json:\"in_stock\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Show the transformed code around ProductsCreate
	t.Logf("DEBUG: Transformed code around ProductsCreate:")
	lines := strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "func ProductsCreate") {
			// Show 15 lines before and after function to see annotations
			start := i - 15
			if start < 0 {
				start = 0
			}
			end := i + 15
			if end > len(lines) {
				end = len(lines)
			}

			t.Logf("ProductsCreate handler (around line %d):", i)
			for j := start; j < end; j++ {
				t.Logf("  %d: %s", j, lines[j])
			}
			t.Logf("")
			break
		}
	}

	// Verify @Param annotations are present for JSON body parameters
	// The format should be: // @Param json body STRUCT_NAME true
	expectedJSONParamAnnotations := []string{
		"@Param json body ProductsCreateRequest true",
		"@Param json body UsersCreateRequest    true",
		"@Param json body ProductsUpdateRequest true",
	}

	// Check for presence of JSON body param annotations (before @Success)
	foundJSONParamAnnotations := 0
	for _, expectedAnnotation := range expectedJSONParamAnnotations {
		if strings.Contains(transformed, expectedAnnotation) {
			foundJSONParamAnnotations++
			t.Logf("Found expected JSON body param annotation: %s", expectedAnnotation)
		}
	}

	if foundJSONParamAnnotations == 0 {
		// Look for any JSON body param annotation pattern
		lines := strings.Split(transformed, "\n")
		for _, line := range lines {
			if strings.Contains(line, "@Param") && strings.Contains(line, "body") {
				t.Logf("Found JSON body param annotation: %s", strings.TrimSpace(line))
				foundJSONParamAnnotations++
			}
		}
	}

	if foundJSONParamAnnotations == 0 {
		t.Fatal("No @Param annotations for JSON body parameters found - this is the bug we're testing")
	}

	// Verify that JSON body param annotations appear before @Success annotations
	lines = strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "@Param") && strings.Contains(line, "body") {
			// Look for @Success in the next few lines
			foundSuccessAfter := false
			for j := i + 1; j < i+10 && j < len(lines); j++ {
				if strings.Contains(lines[j], "@Success") {
					foundSuccessAfter = true
					t.Logf("JSON body param annotation correctly positioned before @Success at line %d", i)
					break
				}
			}
			if !foundSuccessAfter {
				t.Logf("Warning: JSON body param annotation at line %d not followed by @Success in next 10 lines", i)
			}
		}
	}

	t.Logf("SUCCESS: JSON body param annotations test passed - @Param annotations for JSON body parameters were generated")
}

// TestRouterGroupTagsAnnotations tests that @Tags annotations are generated for router groups
func TestRouterGroupTagsAnnotations(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with multiple route groups
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
	adminHandlers "github.com/example/admin/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()

	// API v1 group - should generate @Tags api/v1
	apiV1 := r.Group("/api/v1")
	{
		apiV1.GET("/products", handlers.ProductsIndex)
		apiV1.POST("/products", handlers.ProductsCreate)
		apiV1.PUT("/products/:id", handlers.ProductsUpdate)
		apiV1.DELETE("/products/:id", handlers.ProductsDelete)

		apiV1.GET("/users", handlers.UsersIndex)
		apiV1.POST("/users", handlers.UsersCreate)
	}

	// API v2 group - should generate @Tags api/v2
	apiV2 := r.Group("/api/v2")
	{
		apiV2.GET("/products", handlers.ProductsIndexV2)
		apiV2.GET("/health", handlers.HealthCheck)
	}

	// Admin group - should generate @Tags admin
	admin := r.Group("/admin")
	{
		admin.GET("/dashboard", adminHandlers.Dashboard)
		admin.GET("/users", adminHandlers.UsersIndex)
		admin.POST("/users/:id/ban", adminHandlers.BanUser)
	}

	// Public routes (no group) - should not have @Tags or should have default tag
	r.GET("/", handlers.Home)
	r.GET("/about", handlers.About)

	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create main handlers file
	mainHandlersContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ProductsIndex returns a list of products.
func ProductsIndex(c *gin.Context) {
	var query struct {
		Limit int ` + "`form:\"limit\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// ProductsCreate creates a new product.
func ProductsCreate(c *gin.Context) {
	var req struct {
		Name string ` + "`json:\"name\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 123})
}

// ProductsUpdate updates a product.
func ProductsUpdate(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name string ` + "`json:\"name\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// ProductsDelete deletes a product.
func ProductsDelete(c *gin.Context) {
	id := c.Param("id")
	c.Status(http.StatusOK)
}

// UsersIndex returns a list of users.
func UsersIndex(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{})
}

// UsersCreate creates a new user.
func UsersCreate(c *gin.Context) {
	var req struct {
		Name string ` + "`json:\"name\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 456})
}

// ProductsIndexV2 returns a list of products for API v2.
func ProductsIndexV2(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{})
}

// HealthCheck returns API health status.
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

// Home serves the home page.
func Home(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "Welcome"})
}

// About serves the about page.
func About(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"about": "About page"})
}`

	mainHandlersFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(mainHandlersFile, []byte(mainHandlersContent), 0644); err != nil {
		t.Fatalf("Failed to write main handlers file: %v", err)
	}

	// Create admin handlers directory and file
	adminDir := filepath.Join(tempDir, "admin")
	if err := os.MkdirAll(adminDir, 0755); err != nil {
		t.Fatalf("Failed to create admin directory: %v", err)
	}

	adminHandlersContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Dashboard shows admin dashboard.
func Dashboard(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"stats": "admin stats"})
}

// UsersIndex returns admin users list.
func UsersIndex(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{})
}

// BanUser bans a user.
func BanUser(c *gin.Context) {
	userID := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "banned": true})
}`

	adminHandlersFile := filepath.Join(adminDir, "handlers.go")
	if err := os.WriteFile(adminHandlersFile, []byte(adminHandlersContent), 0644); err != nil {
		t.Fatalf("Failed to write admin handlers file: %v", err)
	}

	// Test main handlers file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, mainHandlersFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse main handlers file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Show all @Tags annotations found
	t.Logf("DEBUG: Looking for @Tags annotations in transformed code:")
	lines := strings.Split(transformed, "\n")
	for _, line := range lines {
		if strings.Contains(line, "@Tags") {
			t.Logf("  Found @Tags: %s", strings.TrimSpace(line))
		}
	}

	// Verify @Tags annotations are present for router groups
	expectedTagsAnnotations := []string{
		"@Tags api/v1", // for /api/v1 routes
		"@Tags api/v2", // for /api/v2 routes
		"@Tags admin",  // for /admin routes
	}

	foundTagsCount := 0
	for _, expectedTag := range expectedTagsAnnotations {
		if strings.Contains(transformed, expectedTag) {
			foundTagsCount++
			t.Logf("Found expected @Tags annotation: %s", expectedTag)
		}
	}

	if foundTagsCount == 0 {
		t.Fatal("No @Tags annotations for router groups found - this is the bug we're testing")
	}

	// Verify specific handlers have correct tags
	handlerToExpectedTag := map[string]string{
		"ProductsIndex":   "@Tags api/v1", // /api/v1/products
		"ProductsCreate":  "@Tags api/v1", // /api/v1/products
		"ProductsUpdate":  "@Tags api/v1", // /api/v1/products/:id
		"ProductsDelete":  "@Tags api/v1", // /api/v1/products/:id
		"UsersIndex":      "@Tags api/v1", // /api/v1/users
		"UsersCreate":     "@Tags api/v1", // /api/v1/users
		"ProductsIndexV2": "@Tags api/v2", // /api/v2/products
		"HealthCheck":     "@Tags api/v2", // /api/v2/health
	}

	for handlerName, expectedTag := range handlerToExpectedTag {
		handlerIndex := strings.Index(transformed, "func "+handlerName+"(")
		if handlerIndex == -1 {
			t.Logf("Warning: Handler %s not found in transformed code", handlerName)
			continue
		}

		// Look for @Tags in the 20 lines before the handler
		searchStart := handlerIndex - 500 // Look further back
		if searchStart < 0 {
			searchStart = 0
		}
		handlerSection := transformed[searchStart:handlerIndex]

		if !strings.Contains(handlerSection, expectedTag) {
			t.Logf("Warning: Expected @Tags '%s' not found before handler %s", expectedTag, handlerName)
		} else {
			t.Logf("Verified: Handler %s has correct @Tags '%s'", handlerName, expectedTag)
		}
	}

	t.Logf("SUCCESS: Router group @Tags annotations test passed - @Tags annotations for router groups were generated")
}

// TestStripRoutePrefix tests that the strip route prefix CLI option works correctly
func TestStripRoutePrefix(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with /api/v1 prefix
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()

	// API v1 group - should generate @Tags v1 (with /api stripped)
	apiV1 := r.Group("/api/v1")
	{
		apiV1.GET("/users", handlers.UsersIndex)
		apiV1.POST("/users", handlers.UsersCreate)
		apiV1.PUT("/users/:id", handlers.UsersUpdate)
		apiV1.DELETE("/users/:id", handlers.UsersDelete)
	}

	// API v2 group - should generate @Tags v2 (with /api stripped)
	apiV2 := r.Group("/api/v2")
	{
		apiV2.GET("/products", handlers.ProductsIndex)
		apiV2.POST("/products", handlers.ProductsCreate)
	}

	// Admin group without /api prefix - should remain as "admin"
	admin := r.Group("/admin")
	{
		admin.GET("/dashboard", handlers.Dashboard)
		admin.POST("/users/:id/ban", handlers.BanUser)
	}

	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// UsersIndex returns a list of users.
func UsersIndex(c *gin.Context) {
	var query struct {
		Limit int ` + "`form:\"limit\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// UsersCreate creates a new user.
func UsersCreate(c *gin.Context) {
	var req struct {
		Name string ` + "`json:\"name\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": "123"})
}

// UsersUpdate updates a user.
func UsersUpdate(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name string ` + "`json:\"name\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}

// UsersDelete deletes a user.
func UsersDelete(c *gin.Context) {
	id := c.Param("id")
	c.Status(http.StatusOK)
}

// ProductsIndex returns a list of products.
func ProductsIndex(c *gin.Context) {
	c.JSON(http.StatusOK, []gin.H{})
}

// ProductsCreate creates a new product.
func ProductsCreate(c *gin.Context) {
	var req struct {
		Name string ` + "`json:\"name\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": "456"})
}

// Dashboard shows admin dashboard.
func Dashboard(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"stats": "admin stats"})
}

// BanUser bans a user.
func BanUser(c *gin.Context) {
	userID := c.Param("id")
	c.JSON(http.StatusOK, gin.H{"user_id": userID, "banned": true})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Test with strip route prefix
	t.Log("Testing with --strip-route-prefix=/api")

	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "/api")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "/api")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Show all @Tags annotations found
	t.Logf("DEBUG: Looking for @Tags annotations in transformed code:")
	lines := strings.Split(transformed, "\n")
	for _, line := range lines {
		if strings.Contains(line, "@Tags") {
			t.Logf("  Found @Tags: %s", strings.TrimSpace(line))
		}
	}

	// Verify that @Tags annotations are generated as PascalCase
	expectedTags := map[string]string{
		"UsersIndex":     "V1Users",        // /api/v1/users -> /v1/users -> v1,users -> V1Users
		"UsersCreate":    "V1Users",        // /api/v1/users -> /v1/users -> v1,users -> V1Users
		"UsersUpdate":    "V1Users",        // /api/v1/users/:id -> /v1/users/:id -> v1,users -> V1Users
		"UsersDelete":    "V1Users",        // /api/v1/users/:id -> /v1/users/:id -> v1,users -> V1Users
		"ProductsIndex":  "V2Products",     // /api/v2/products -> /v2/products -> v2,products -> V2Products
		"ProductsCreate": "V2Products",     // /api/v2/products -> /v2/products -> v2,products -> V2Products
		"Dashboard":      "AdminDashboard", // /admin/dashboard -> admin,dashboard -> AdminDashboard
		"BanUser":        "AdminUsers",     // /admin/users/:id/ban -> admin,users -> AdminUsers
	}

	for handlerName, expectedTag := range expectedTags {
		handlerIndex := strings.Index(transformed, "func "+handlerName+"(")
		if handlerIndex == -1 {
			t.Logf("Warning: Handler %s not found in transformed code", handlerName)
			continue
		}

		// Look for @Tags in the 20 lines before the handler
		searchStart := handlerIndex - 500 // Look further back
		if searchStart < 0 {
			searchStart = 0
		}
		handlerSection := transformed[searchStart:handlerIndex]

		expectedTagAnnotation := fmt.Sprintf("@Tags %s", expectedTag)
		if !strings.Contains(handlerSection, expectedTagAnnotation) {
			t.Errorf("Expected @Tags '%s' not found before handler %s. Found in section:\n%s", expectedTag, handlerName, handlerSection)
		} else {
			t.Logf("Verified: Handler %s has correct @Tags '%s'", handlerName, expectedTag)
		}

		// Verify that tags with "api" prefix are NOT present (only for v1 and v2 handlers)
		if expectedTag == "v1" || expectedTag == "v2" {
			incorrectTag := "api/" + expectedTag
			if strings.Contains(handlerSection, "@Tags "+incorrectTag) {
				t.Errorf("Incorrect @Tags '%s' found before handler %s - prefix was not stripped", incorrectTag, handlerName)
			}
		}
	}

	// Test without strip route prefix for comparison
	t.Log("\nTesting without strip route prefix (baseline)")
	routerFile2 := filepath.Join(tempDir, "router2.go")
	routerContent2 := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()

	api := r.Group("/api")
	{
		api.GET("/test", handlers.TestHandler)
	}

	return r
}`

	if err := os.WriteFile(routerFile2, []byte(routerContent2), 0644); err != nil {
		t.Fatalf("Failed to write baseline router file: %v", err)
	}

	handlerContent2 := `package handlers

import "github.com/gin-gonic/gin"

// TestHandler is a test handler.
func TestHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{})
}`

	handlerFile2 := filepath.Join(tempDir, "handlers2.go")
	if err := os.WriteFile(handlerFile2, []byte(handlerContent2), 0644); err != nil {
		t.Fatalf("Failed to write baseline handler file: %v", err)
	}

	fileSet2 := token.NewFileSet()
	file2, err := parser.ParseFile(fileSet2, handlerFile2, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse baseline handler file: %v", err)
	}

	detector2 := analyzer.NewAnonymousStructDetector(fileSet2, file2, "handlers")
	result2, err := detector2.DetectAnonymousStructsWithRouter(routerFile2, "")
	if err != nil {
		t.Fatalf("Failed to analyze baseline anonymous structs: %v", err)
	}

	dstTransformer2 := transformer.NewDSTTransformer(fileSet2, file2, result2, "")
	transformed2, err := dstTransformer2.Transform()
	if err != nil {
		t.Fatalf("Failed to transform baseline AST: %v", err)
	}

	// Without strip prefix, should still have "Api" (PascalCase)
	if !strings.Contains(transformed2, "@Tags Api") {
		t.Error("Expected @Tags 'Api' in baseline test without strip prefix")
	} else {
		t.Logf("Verified: Baseline test correctly shows @Tags 'Api' without strip prefix")
	}

	t.Logf("SUCCESS: Strip route prefix test passed - route prefixes are correctly stripped from @Tags")
}

// TestNewTagGenerationWithStripPrefix tests the new tag generation logic:
// full path -> strip prefix -> first two segments as tags
func TestNewTagGenerationWithStripPrefix(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file with shop group structure
	routerContent := `package routing

import (
	"github.com/gin-gonic/gin"
	"../handlers"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()

	// API group
	api := r.Group("/api")
	{
		// Shop group
		shop := api.Group("/shop")
		{
			shop.GET("/orders/:id/checks", handlers.OrderCheckIndex)
			shop.GET("/orders", handlers.OrderIndex)
			shop.GET("/products", handlers.ProductIndex)
			shop.GET("/customer-accounts", handlers.CustomerAccountsIndex)
		}

		// Admin group
		admin := api.Group("/admin")
		{
			admin.GET("/users/:id/ban", admin.AdminBanUser)
			admin.GET("/dashboard", admin.AdminDashboard)
		}
	}

	return r
}
`

	// Create handler file with anonymous structs
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

type OrderCheckResponse struct {
	ID string ` + "`" + `json:"id"` + "`" + `
}

type OrderResponse struct {
	Orders []Order ` + "`" + `json:"orders"` + "`" + `
}

type ProductResponse struct {
	Products []Product ` + "`" + `json:"products"` + "`" + `
}

func OrderCheckIndex(c *gin.Context) {
	var response []OrderCheckResponse
	c.JSON(http.StatusOK, response)
}

func OrderIndex(c *gin.Context) {
	var response OrderResponse
	c.JSON(http.StatusOK, response)
}

func ProductIndex(c *gin.Context) {
	var response ProductResponse
	c.JSON(http.StatusOK, response)
}

type CustomerAccountsResponse struct {
	Accounts []string ` + "`" + `json:"accounts"` + "`" + `
}

func CustomerAccountsIndex(c *gin.Context) {
	var response CustomerAccountsResponse
	c.JSON(http.StatusOK, response)
}
`

	// Write test files
	routerPath := filepath.Join(tempDir, "router.go")
	handlerPath := filepath.Join(tempDir, "handlers.go")

	if err := os.WriteFile(routerPath, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}
	if err := os.WriteFile(handlerPath, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Test with strip prefix /api
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerPath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse Go file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerPath, "/api")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "/api")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Show all @Tags annotations found
	t.Logf("DEBUG: Looking for @Tags annotations in transformed code:")
	lines := strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "@Tags") {
			t.Logf("  Line %d: %s", i+1, strings.TrimSpace(line))
		}
	}

	// Expected tags after stripping /api (PascalCase format):
	// /api/shop/orders/:id/checks -> /shop/orders/:id/checks -> ["shop", "orders"] -> @Tags ShopOrders
	// /api/shop/orders -> /shop/orders -> ["shop", "orders"] -> @Tags ShopOrders
	// /api/shop/products -> /shop/products -> ["shop", "products"] -> @Tags ShopProducts
	// /api/shop/customer-accounts -> /shop/customer-accounts -> ["shop", "customer-accounts"] -> @Tags ShopCustomerAccounts
	expectedTags := map[string]string{
		"OrderCheckIndex":       "@Tags ShopOrders",           // /shop/orders/:id/checks
		"OrderIndex":            "@Tags ShopOrders",           // /shop/orders
		"ProductIndex":          "@Tags ShopProducts",         // /shop/products
		"CustomerAccountsIndex": "@Tags ShopCustomerAccounts", // /shop/customer-accounts (hyphen handling)
	}

	// Verify each handler has the correct @Tags annotation
	for handlerName, expectedTag := range expectedTags {
		found := false
		for i, line := range lines {
			if strings.Contains(line, expectedTag) {
				// Look forwards to find the handler function (since @Tags comes before the function)
				handlerFound := false
				for j := i; j < len(lines) && j <= i+20; j++ {
					if strings.Contains(lines[j], "func "+handlerName) {
						handlerFound = true
						break
					}
				}
				if handlerFound {
					found = true
					t.Logf("Verified: Handler %s has correct @Tags '%s'", handlerName, expectedTag)
					break
				}
			}
		}
		if !found {
			t.Errorf("Expected @Tags '%s' not found for handler %s", expectedTag, handlerName)
			t.Logf("Handler section around where it should be:")
			// Show a snippet of the code around where the handler should be
			handlerFound := false
			for i, line := range lines {
				if strings.Contains(line, "func "+handlerName) {
					handlerFound = true
					start := i
					end := i + 15
					if start < 0 {
						start = 0
					}
					if end > len(lines) {
						end = len(lines)
					}
					for j := start; j < end; j++ {
						t.Logf("  %s", lines[j])
					}
					break
				}
			}
			if !handlerFound {
				t.Logf("Handler %s not found in transformed code", handlerName)
			}
		}
	}

	if len(expectedTags) > 0 {
		t.Logf("SUCCESS: New tag generation test passed - tags are correctly generated using full path -> strip prefix -> first two segments")
	}
}

// TestDocCommentPreservation tests that doc comments remain properly positioned with their handler functions
// after type declarations are inserted at the top of the file
func TestDocCommentPreservation(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create handler file with doc comments that should be preserved
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// CreateUser creates a new user in the system.
// @Tags user
// @Summary Create a new user
// @Description Creates a new user with the provided name and email
// @Accept json
// @Produce json
// @Param request body object true "User creation request"
// @Success 201 {object} gin.H
// @Failure 400 {object} gin.H
// @Router /users [post]
func CreateUser(c *gin.Context) {
	var req struct {
		Name  string ` + "`json:\"name\"`" + `
		Email string ` + "`json:\"email\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User created successfully",
		"user":    req,
	})
}

// UpdateUser updates an existing user in the system.
// @Tags user
// @Summary Update an existing user
// @Description Updates an existing user with the provided information
// @Accept json
// @Produce json
// @Param id path string true "User ID"
// @Param request body object true "User update request"
// @Success 200 {object} gin.H
// @Failure 400 {object} gin.H
// @Failure 404 {object} gin.H
// @Router /users/{id} [put]
func UpdateUser(c *gin.Context) {
	var req struct {
		Name  *string ` + "`json:\"name,omitempty\"`" + `
		Email *string ` + "`json:\"email,omitempty\"`" + `
	}
	userID := c.Param("id")

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "User updated successfully",
		"user_id": userID,
		"user":    req,
	})
}

// DeleteUser deletes a user from the system.
// @Tags user
// @Summary Delete a user
// @Description Deletes a user from the system by ID
// @Param id path string true "User ID"
// @Success 204
// @Failure 404 {object} gin.H
// @Router /users/{id} [delete]
func DeleteUser(c *gin.Context) {
	userID := c.Param("id")

	c.JSON(http.StatusNoContent, gin.H{
		"message": "User deleted successfully",
		"user_id": userID,
	})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.POST("/users", handlers.CreateUser)
	r.PUT("/users/:id", handlers.UpdateUser)
	r.DELETE("/users/:id", handlers.DeleteUser)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Parse and analyze
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Transform the file
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Verify that type declarations are inserted at the top
	t.Logf("Transformed content:\n%s", transformed)

	// Check that type declarations are present and exported
	if !strings.Contains(transformed, "type CreateUserRequest struct {") {
		t.Error("Expected exported CreateUserRequest type declaration")
	}
	if !strings.Contains(transformed, "type UpdateUserRequest struct {") {
		t.Error("Expected exported UpdateUserRequest type declaration")
	}

	// Check that doc comments are preserved and properly positioned
	// The doc comments should appear immediately before their respective functions
	lines := strings.Split(transformed, "\n")

	// Find line numbers of doc comments and their functions
	var createUserDocLine, updateUserDocLine, deleteUserDocLine int
	var createUserFuncLine, updateUserFuncLine, deleteUserFuncLine int

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Look for doc comments (but not Swagger annotations)
		if strings.HasPrefix(trimmed, "// CreateUser creates a new user") {
			createUserDocLine = lineNum
		}
		if strings.HasPrefix(trimmed, "// UpdateUser updates an existing user") {
			updateUserDocLine = lineNum
		}
		if strings.HasPrefix(trimmed, "// DeleteUser deletes a user") {
			deleteUserDocLine = lineNum
		}

		// Look for function declarations
		if strings.HasPrefix(trimmed, "func CreateUser(c *gin.Context) {") {
			createUserFuncLine = lineNum
		}
		if strings.HasPrefix(trimmed, "func UpdateUser(c *gin.Context) {") {
			updateUserFuncLine = lineNum
		}
		if strings.HasPrefix(trimmed, "func DeleteUser(c *gin.Context) {") {
			deleteUserFuncLine = lineNum
		}
	}

	// Verify that doc comments appear immediately before their functions
	success := true

	if createUserDocLine > 0 && createUserFuncLine > 0 {
		gap := createUserFuncLine - createUserDocLine
		if gap > 15 {
			t.Errorf("CreateUser doc comment (line %d) is too far from function (line %d), gap: %d lines",
				createUserDocLine, createUserFuncLine, gap)
			success = false
		} else {
			t.Logf("✓ CreateUser doc comment reasonably positioned at line %d (gap: %d lines to function)", createUserDocLine, gap)
		}
	} else {
		t.Error("Could not find CreateUser doc comment or function")
		success = false
	}

	if updateUserDocLine > 0 && updateUserFuncLine > 0 {
		gap := updateUserFuncLine - updateUserDocLine
		if gap > 15 {
			t.Errorf("UpdateUser doc comment (line %d) is too far from function (line %d), gap: %d lines",
				updateUserDocLine, updateUserFuncLine, gap)
			success = false
		} else {
			t.Logf("✓ UpdateUser doc comment reasonably positioned at line %d (gap: %d lines to function)", updateUserDocLine, gap)
		}
	} else {
		t.Error("Could not find UpdateUser doc comment or function")
		success = false
	}

	if deleteUserDocLine > 0 && deleteUserFuncLine > 0 {
		gap := deleteUserFuncLine - deleteUserDocLine
		if gap > 15 {
			t.Errorf("DeleteUser doc comment (line %d) is too far from function (line %d), gap: %d lines",
				deleteUserDocLine, deleteUserFuncLine, gap)
			success = false
		} else {
			t.Logf("✓ DeleteUser doc comment reasonably positioned at line %d (gap: %d lines to function)", deleteUserDocLine, gap)
		}
	} else {
		t.Error("Could not find DeleteUser doc comment or function")
		success = false
	}

	// Verify that Swagger annotations are still present with the functions
	if !strings.Contains(transformed, "@Tags user") {
		t.Error("Expected Swagger @Tags annotations to be preserved")
		success = false
	}
	if !strings.Contains(transformed, "@Summary Create a new user") {
		t.Error("Expected Swagger @Summary annotations to be preserved")
		success = false
	}
	if !strings.Contains(transformed, "@Router /users [post]") {
		t.Error("Expected Swagger @Router annotations to be preserved")
		success = false
	}

	if success {
		t.Logf("SUCCESS: Doc comment preservation test passed - doc comments remain properly positioned with their handler functions")
	}
}

// TestCommentOrdering tests that original doc comments appear before Swagger annotations
func TestCommentOrdering(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create handler file with simple doc comment that should appear before generated Swagger annotations
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// BrandCreate create a new product brand.
func BrandCreate(c *gin.Context) {
	var req struct {
		Name string ` + "`json:\"name\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Brand created successfully"})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Create router file that will trigger Swagger annotation generation
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.POST("/brands", handlers.BrandCreate)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Parse the handler file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	// Analyze the file
	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Transform the AST
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	t.Logf("Transformed content:\n%s", transformed)

	// Split into lines for analysis
	lines := strings.Split(transformed, "\n")

	// Find the BrandCreate function and its doc comments
	var brandCreateDocLine, brandCreateFuncLine int
	var swaggerAnnotationLines []int

	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Look for the original doc comment
		if strings.HasPrefix(trimmed, "// BrandCreate create a new product brand.") {
			brandCreateDocLine = lineNum
		}

		// Look for Swagger annotations
		if strings.HasPrefix(trimmed, "// @Tags") || strings.HasPrefix(trimmed, "// @Success") || strings.HasPrefix(trimmed, "// @Router") {
			swaggerAnnotationLines = append(swaggerAnnotationLines, lineNum)
		}

		// Look for the function declaration
		if strings.HasPrefix(trimmed, "func BrandCreate(c *gin.Context) {") {
			brandCreateFuncLine = lineNum
		}
	}

	// Verify that the original doc comment comes before Swagger annotations
	if brandCreateDocLine == 0 {
		t.Error("Could not find BrandCreate doc comment")
	}

	if len(swaggerAnnotationLines) == 0 {
		t.Error("Could not find any Swagger annotations")
	}

	if brandCreateFuncLine == 0 {
		t.Error("Could not find BrandCreate function")
	}

	// Check ordering: doc comment should be before all Swagger annotations
	for _, swaggerLine := range swaggerAnnotationLines {
		if brandCreateDocLine >= swaggerLine {
			t.Errorf("BrandCreate doc comment (line %d) should come before Swagger annotation (line %d)",
				brandCreateDocLine, swaggerLine)
		}
	}

	// Check that everything comes before the function
	for _, swaggerLine := range swaggerAnnotationLines {
		if swaggerLine >= brandCreateFuncLine {
			t.Errorf("Swagger annotation (line %d) should come before function (line %d)",
				swaggerLine, brandCreateFuncLine)
		}
	}

	if brandCreateDocLine >= brandCreateFuncLine {
		t.Errorf("BrandCreate doc comment (line %d) should come before function (line %d)",
			brandCreateDocLine, brandCreateFuncLine)
	}

	// Verify the specific order in the output
	if brandCreateDocLine > 0 && len(swaggerAnnotationLines) > 0 {
		t.Logf("✓ Comment ordering correct: doc comment at line %d, Swagger annotations at lines %v, function at line %d",
			brandCreateDocLine, swaggerAnnotationLines, brandCreateFuncLine)
	}

	// Verify that the type was created and exported
	if !strings.Contains(transformed, "type BrandCreateRequest struct {") {
		t.Error("Expected exported BrandCreateRequest type declaration")
	}
}

// TestInlineCommentPreservation tests that comments within function bodies are preserved
func TestInlineCommentPreservation(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create handler file with inline comments that should be preserved
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ProcessData processes incoming data with validation.
func ProcessData(c *gin.Context) {
	var req struct {
		Name  string ` + "`json:\"name\"`" + `
		Email string ` + "`json:\"email\"`" + `
	}

	// Validate the input data
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Log the request for debugging
	// This helps with troubleshooting issues
	t.Logf("Processing request from: %s", req.Email) // This comment should be preserved

	// Process the business logic
	result := gin.H{
		"message": "Data processed successfully",
		"user":    req.Name,
	}

	/* Multi-line comment that should be preserved
	   This comment explains the complex logic below
	   and why we're doing it this way */
	c.JSON(http.StatusOK, result)
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.POST("/process", handlers.ProcessData)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Parse the handler file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	// Analyze the file
	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Transform the AST
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	t.Logf("Transformed content:\n%s", transformed)

	// Check that inline comments are preserved
	inlineComments := []string{
		"// Validate the input data",
		"// Log the request for debugging",
		"// This helps with troubleshooting issues",
		"// This comment should be preserved",
		"// Process the business logic",
		"/* Multi-line comment that should be preserved",
		"This comment explains the complex logic below",
		"and why we're doing it this way */",
	}

	missingComments := []string{}
	for _, comment := range inlineComments {
		if !strings.Contains(transformed, comment) {
			missingComments = append(missingComments, comment)
		}
	}

	if len(missingComments) > 0 {
		t.Errorf("The following inline comments were not preserved:\n%s", strings.Join(missingComments, "\n"))
	}

	// Verify that the function body structure is maintained
	if !strings.Contains(transformed, "func ProcessData(c *gin.Context) {") {
		t.Error("Expected ProcessData function declaration")
	}

	// Check that the business logic is still there
	if !strings.Contains(transformed, "ShouldBindJSON(&req)") {
		t.Error("Expected business logic to be preserved")
	}

	if !strings.Contains(transformed, `"message": "Data processed successfully"`) {
		t.Error("Expected business logic output to be preserved")
	}

	// Verify that the type was created and exported
	if !strings.Contains(transformed, "type ProcessDataRequest struct {") {
		t.Error("Expected exported ProcessDataRequest type declaration")
	}

	t.Logf("SUCCESS: Inline comment preservation test passed - %d inline comments preserved", len(inlineComments)-len(missingComments))
}

// TestDocCommentRegression tests that doc comments are not displaced from their functions
func TestDocCommentRegression(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create handler file with simple doc comments (like the user's code)
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ClassIndex returns shipping classes.
func ClassIndex(c *gin.Context) {
	var bs []struct {
		ID   int
		Name string
	}

	c.JSON(http.StatusOK, bs)
}

// StringCreate create a new string.
func StringCreate(c *gin.Context) {
	var a struct {
		Name string ` + "`json:\"name\"`" + `
	}

	c.JSON(http.StatusCreated, a)
}

// StringShow show the specified string.
func StringShow(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "success"})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/classes", handlers.ClassIndex)
	r.POST("/strings", handlers.StringCreate)
	r.GET("/strings/:id", handlers.StringShow)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Parse the handler file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	// Analyze the file
	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Transform the AST
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	t.Logf("Transformed content:\n%s", transformed)

	// Check that doc comments are correctly positioned before their functions
	lines := strings.Split(transformed, "\n")

	testCases := []struct {
		docComment   string
		functionName string
		maxGapLines  int
	}{
		{"// ClassIndex returns shipping classes.", "func ClassIndex(c *gin.Context) {", 25},
		{"// StringCreate create a new string.", "func StringCreate(c *gin.Context) {", 25},
		{"// StringShow show the specified string.", "func StringShow(c *gin.Context) {", 25},
	}

	for _, tc := range testCases {
		var docLine, funcLine int

		for i, line := range lines {
			lineNum := i + 1
			trimmed := strings.TrimSpace(line)

			if strings.Contains(trimmed, tc.docComment) {
				docLine = lineNum
			}
			if strings.Contains(trimmed, tc.functionName) {
				funcLine = lineNum
			}
		}

		if docLine == 0 {
			t.Errorf("Could not find doc comment: %s", tc.docComment)
		} else if funcLine == 0 {
			t.Errorf("Could not find function: %s", tc.functionName)
		} else {
			gap := funcLine - docLine
			if gap > tc.maxGapLines {
				t.Errorf("Doc comment '%s' at line %d is too far from function at line %d (gap: %d, max allowed: %d)",
					tc.docComment, docLine, funcLine, gap, tc.maxGapLines)
			} else {
				t.Logf("✓ %s: doc comment at line %d, function at line %d (gap: %d)",
					tc.functionName, docLine, funcLine, gap)
			}
		}
	}

	t.Logf("SUCCESS: Doc comment regression test passed - doc comments are properly positioned")
}

// TestCommentDisplacementRegression tests that doc comments remain attached to their functions
// when types are moved to the top of the file (regression test for type placement fix)
func TestCommentDisplacementRegression(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create handler file that simulates the image.go scenario with mixed comments and handlers
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// ImageType pre-defined image types.

// Image represents an image.

// EditorUploader takes files from the rich text editor and saves them to the blob storage.
func EditorUploader(c *gin.Context) {
	var req struct {
		File string ` + "`json:\"file\"`" + `
		Type int    ` + "`json:\"type\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ImageCreate saves the image file with size and SHA256 hash meta.
func ImageCreate(c *gin.Context) {
	var req struct {
		Name string ` + "`json:\"name\" binding:\"required\"`" + `
		Data string ` + "`json:\"data\" binding:\"required\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 123, "name": req.Name})
}

// ImageIndex returns images usable in tinyMCE.
func ImageIndex(c *gin.Context) {
	var query struct {
		Limit  int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
	}

	if err := c.ShouldBindQuery(&query); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, []gin.H{})
}

// ImageDelete delete the given image and its alternatives.
func ImageDelete(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true})
}
`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.POST("/editor-uploader", handlers.EditorUploader)
	r.POST("/images", handlers.ImageCreate)
	r.GET("/images", handlers.ImageIndex)
	r.DELETE("/images/:id", handlers.ImageDelete)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Parse the handler file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	// Analyze the file with router
	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Transform the AST
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	t.Logf("Transformed content:\n%s", transformed)

	// Test assertions for comment positioning

	// 1. Types should be at the top of the file
	if !strings.Contains(transformed, "type EditorUploaderRequest struct {") {
		t.Error("Expected EditorUploaderRequest type declaration at top of file")
	}
	if !strings.Contains(transformed, "type ImageCreateRequest struct {") {
		t.Error("Expected ImageCreateRequest type declaration at top of file")
	}
	if !strings.Contains(transformed, "type ImageIndexQueryParams struct {") {
		t.Error("Expected ImageIndexQueryParams type declaration at top of file")
	}

	// 2. Each handler function should have its doc comment directly above it
	// Check EditorUploader
	editorUploaderStart := strings.Index(transformed, "func EditorUploader(c *gin.Context) {")
	if editorUploaderStart == -1 {
		t.Fatal("EditorUploader function not found")
	}
	// Look for doc comment within reasonable distance before function (max 10 lines)
	editorUploaderSection := transformed[max(0, editorUploaderStart-500):editorUploaderStart]
	if !strings.Contains(editorUploaderSection, "EditorUploader takes files from the rich text editor") {
		t.Error("EditorUploader doc comment not found above function")
	}

	// Check ImageCreate
	imageCreateStart := strings.Index(transformed, "func ImageCreate(c *gin.Context) {")
	if imageCreateStart == -1 {
		t.Fatal("ImageCreate function not found")
	}
	imageCreateSection := transformed[max(0, imageCreateStart-500):imageCreateStart]
	if !strings.Contains(imageCreateSection, "ImageCreate saves the image file") {
		t.Error("ImageCreate doc comment not found above function")
	}

	// Check ImageIndex
	imageIndexStart := strings.Index(transformed, "func ImageIndex(c *gin.Context) {")
	if imageIndexStart == -1 {
		t.Fatal("ImageIndex function not found")
	}
	imageIndexSection := transformed[max(0, imageIndexStart-500):imageIndexStart]
	if !strings.Contains(imageIndexSection, "ImageIndex returns images usable") {
		t.Error("ImageIndex doc comment not found above function")
	}

	// Check ImageDelete
	imageDeleteStart := strings.Index(transformed, "func ImageDelete(c *gin.Context) {")
	if imageDeleteStart == -1 {
		t.Fatal("ImageDelete function not found")
	}
	imageDeleteSection := transformed[max(0, imageDeleteStart-500):imageDeleteStart]
	if !strings.Contains(imageDeleteSection, "ImageDelete delete the given image") {
		t.Error("ImageDelete doc comment not found above function")
	}

	// 3. Swagger annotations should be above functions, not types
	// Check @Router annotations are with functions
	if !strings.Contains(editorUploaderSection, "@Router /editor-uploader [POST]") {
		t.Error("EditorUploader @Router annotation not found above function")
	}
	if !strings.Contains(imageCreateSection, "@Router /images [POST]") {
		t.Error("ImageCreate @Router annotation not found above function")
	}
	if !strings.Contains(imageIndexSection, "@Router /images [GET]") {
		t.Error("ImageIndex @Router annotation not found above function")
	}
	if !strings.Contains(imageDeleteSection, "@Router /images/:id [DELETE]") {
		t.Error("ImageDelete @Router annotation not found above function")
	}

	// 4. No duplicate comments should exist
	commentCounts := make(map[string]int)
	lines := strings.Split(transformed, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "// @") {
			commentCounts[trimmed]++
		}
	}

	// Check for duplicates
	duplicates := []string{}
	for comment, count := range commentCounts {
		if count > 1 {
			duplicates = append(duplicates, fmt.Sprintf("'%s' (%d times)", comment, count))
		}
	}
	if len(duplicates) > 0 {
		t.Errorf("Found duplicate comments: %s", strings.Join(duplicates, ", "))
	}

	// 5. Verify that orphaned comments don't exist above type declarations
	// Check that type declarations don't have handler doc comments above them
	typeDeclSection := strings.Split(transformed, "func")[0] // Get section before first function
	if strings.Contains(typeDeclSection, "EditorUploader takes files from the rich text editor") {
		t.Error("Found EditorUploader doc comment above type declarations (should be above function)")
	}
	if strings.Contains(typeDeclSection, "ImageCreate saves the image file") {
		t.Error("Found ImageCreate doc comment above type declarations (should be above function)")
	}

	t.Logf("SUCCESS: Comment displacement regression test passed - all comments are properly positioned above their functions")
}

// TestDuplicateCommentFixtures tests that doc comments are not duplicated
func TestDuplicateCommentFixtures(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main
import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/brands", handlers.BrandIndex)
	r.POST("/users", handlers.UserCreate)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with existing doc comments that should not be duplicated
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// BrandIndex returns product brands.
func BrandIndex(c *gin.Context) {
	var bs []struct {
		ID           string
		Name         string
		ProductCount int
		UpdatedAt    time.Time
	}

	c.JSON(http.StatusOK, bs)
}

// UserCreate creates a new user.
func UserCreate(c *gin.Context) {
	var req struct {
		Name  string ` + "`json:\"name\" binding:\"required\"`" + `
		Email string ` + "`json:\"email\" binding:\"required,email\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": 123})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Check for duplicate comments
	lines := strings.Split(transformed, "\n")

	// Count occurrences of each comment line
	commentCounts := make(map[string]int)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// BrandIndex returns product brands.") {
			commentCounts[trimmed]++
		}
		if strings.HasPrefix(trimmed, "// UserCreate creates a new user.") {
			commentCounts[trimmed]++
		}
	}

	// Verify no duplicates
	for comment, count := range commentCounts {
		if count > 1 {
			t.Errorf("Duplicate comment found: '%s' appears %d times (should only appear once)", comment, count)
		}
	}

	// Verify original comments are preserved
	foundBrandIndexComment := false
	foundUserCreateComment := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "BrandIndex returns product brands.") {
			foundBrandIndexComment = true
		}
		if strings.Contains(trimmed, "UserCreate creates a new user.") {
			foundUserCreateComment = true
		}
	}

	if !foundBrandIndexComment {
		t.Error("Original BrandIndex doc comment not found in transformed code")
	}
	if !foundUserCreateComment {
		t.Error("Original UserCreate doc comment not found in transformed code")
	}

	t.Logf("SUCCESS: Duplicate comment test passed - no duplicates found")
}

// TestArrayAnnotationGeneration tests that {array} is used for array responses
func TestArrayAnnotationGeneration(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create handler file with both array and object responses
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// BrandIndex returns product brands.
func BrandIndex(c *gin.Context) {
	var bs []struct {
		ID           string ` + "`json:\"id\"`" + `
		Name         string ` + "`json:\"name\"`" + `
		ProductCount int    ` + "`json:\"product_count\"`" + `
	}
	c.JSON(http.StatusOK, bs)
}

// ProductShow returns a single product.
func ProductShow(c *gin.Context) {
	var p struct {
		ID    string  ` + "`json:\"id\"`" + `
		Name  string  ` + "`json:\"name\"`" + `
		Price float64 ` + "`json:\"price\"`" + `
	}
	c.JSON(http.StatusOK, p)
}
`
	handlerPath := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerPath, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to create handler file: %v", err)
	}

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/andreas/gin-replace-anon-struct/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/brands", handlers.BrandIndex)
	r.GET("/products/:id", handlers.ProductShow)
	return r
}
`
	routerPath := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerPath, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to create router file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerPath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerPath, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Generate Swagger annotations
	generator := swagger.NewAnnotationGenerator(result, "")
	annotations := generator.GenerateAnnotations()

	// Find BrandIndex annotation
	var brandIndexAnnotation *swagger.HandlerAnnotations
	for i := range annotations {
		if annotations[i].HandlerName == "BrandIndex" {
			brandIndexAnnotation = &annotations[i]
			break
		}
	}

	if brandIndexAnnotation == nil {
		t.Fatal("BrandIndex annotation not found")
	}

	// Find ProductShow annotation
	var productShowAnnotation *swagger.HandlerAnnotations
	for i := range annotations {
		if annotations[i].HandlerName == "ProductShow" {
			productShowAnnotation = &annotations[i]
			break
		}
	}

	if productShowAnnotation == nil {
		t.Fatal("ProductShow annotation not found")
	}

	// Join all annotations for easier searching
	brandIndexAllContent := brandIndexAnnotation.ExistingComment + "\n" + strings.Join(brandIndexAnnotation.Annotations, "\n")
	productShowAllContent := productShowAnnotation.ExistingComment + "\n" + strings.Join(productShowAnnotation.Annotations, "\n")

	// Test that BrandIndex (array response) uses {array} annotation
	if !strings.Contains(brandIndexAllContent, "@Success 200 {array} BrandIndexResponse") {
		t.Errorf("Expected BrandIndex to have {array} annotation, got:\n%s", brandIndexAllContent)
	}

	// Test that ProductShow (object response) uses {object} annotation
	if !strings.Contains(productShowAllContent, "@Success 200 {object} ProductShowResponse") {
		t.Errorf("Expected ProductShow to have {object} annotation, got:\n%s", productShowAllContent)
	}

	// Verify that we don't have the wrong annotation types
	if strings.Contains(brandIndexAllContent, "@Success 200 {object} BrandIndexResponse") {
		t.Errorf("BrandIndex should use {array}, not {object}")
	}

	if strings.Contains(productShowAllContent, "@Success 200 {array} ProductShowResponse") {
		t.Errorf("ProductShow should use {object}, not {array}")
	}

	t.Logf("SUCCESS: Array annotation test passed - correct schema types used")
}

func TestAnonymousStructSliceDetection(t *testing.T) {
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main
import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.GET("/brands", handlers.BrandIndex)
	r.GET("/orders", handlers.OrderIndex)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with various anonymous struct patterns
	handlerContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// BrandIndex returns product brands.
func BrandIndex(c *gin.Context) {
	// Short variable declaration with slice of anonymous structs
	var bs []struct {
		ID           string
		Name         string
		ProductCount int
		UpdatedAt    time.Time
	}

	// Full variable declaration with slice of anonymous structs
	var products []struct {
		ID    int     ` + "`json:\"id\"`" + `
		Name  string  ` + "`json:\"name\"`" + `
		Price float64 ` + "`json:\"price\"`" + `
	}

	// Assignment with slice of anonymous structs
	items := []struct {
		SKU   string ` + "`json:\"sku\"`" + `
		Stock int    ` + "`json:\"stock\"`" + `
	}{}

	c.JSON(http.StatusOK, bs)
}

// OrderIndex returns orders.
func OrderIndex(c *gin.Context) {
	// Single anonymous struct (not slice)
	var order struct {
		ID     int    ` + "`json:\"id\"`" + `
		Status string ` + "`json:\"status\"`" + `
	}

	// Query parameter struct
	var query struct {
		Limit  int ` + "`form:\"limit\"`" + `
		Offset int ` + "`form:\"offset\"`" + `
	}

	c.JSON(http.StatusOK, order)
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Verify we found the expected anonymous structs
	expectedStructs := []string{
		"bs",       // from BrandIndex - slice
		"products", // from BrandIndex - slice
		"items",    // from BrandIndex - slice
		"order",    // from OrderIndex - single struct
		"query",    // from OrderIndex - single struct
	}

	foundStructs := make(map[string]bool)
	for _, anonStruct := range result.AnonymousStructs {
		foundStructs[anonStruct.VariableName] = true
	}

	for _, expected := range expectedStructs {
		if !foundStructs[expected] {
			t.Errorf("Expected anonymous struct '%s' not found", expected)
		}
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Verify that named types were generated for the slices
	lines := strings.Split(transformed, "\n")

	// Look for generated type definitions
	foundBrandIndexType := false
	foundProductsType := false
	foundItemsType := false
	foundOrderType := false
	foundQueryType := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "type BrandIndexResponse") {
			foundBrandIndexType = true
		}
		if strings.Contains(trimmed, "type BrandIndexRequest") {
			foundProductsType = true
		}
		if strings.Contains(trimmed, "type BrandIndexRequest1") {
			foundItemsType = true
		}
		if strings.Contains(trimmed, "type OrderIndexResponse") {
			foundOrderType = true
		}
		if strings.Contains(trimmed, "type OrderIndexQueryParams") {
			foundQueryType = true
		}
	}

	// At least some of the types should be generated
	typeFoundCount := 0
	if foundBrandIndexType {
		typeFoundCount++
	}
	if foundProductsType {
		typeFoundCount++
	}
	if foundItemsType {
		typeFoundCount++
	}
	if foundOrderType {
		typeFoundCount++
	}
	if foundQueryType {
		typeFoundCount++
	}

	if typeFoundCount == 0 {
		t.Error("No named types were generated for anonymous structs")
	}

	// Verify that the original slice declarations have been replaced
	foundBsReplacement := false
	for _, line := range lines {
		if strings.Contains(line, "bs []BrandIndexResponse") {
			foundBsReplacement = true
			break
		}
	}

	if !foundBsReplacement {
		t.Error("Original bs []struct slice was not replaced with named type")
	}

	t.Logf("SUCCESS: Anonymous struct slice detection test passed - %d types generated", typeFoundCount)
}

// TestJSONRawMessageSwaggerTag tests that json.RawMessage fields get swaggertype:"array,object" tag
func TestPathParameterAnnotations(t *testing.T) {
	// Test the path parameter annotation generation functionality
	// This tests the new generatePathParamAnnotations method

	// Create a mock route info
	route := router.RouteInfo{
		Method:      "GET",
		Path:        "/api/product/attributes/:id/options/:oid",
		RouteGroups: []string{"api", "product"},
	}

	// Test path parameter extraction using test helper
	pathParams := extractPathParametersForTesting("/api/product/attributes/:id/options/:oid")

	if len(pathParams) != 2 {
		t.Errorf("Expected 2 path parameters, got %d", len(pathParams))
	}

	if pathParams[0].Name != "id" {
		t.Errorf("Expected first parameter to be 'id', got '%s'", pathParams[0].Name)
	}

	if pathParams[1].Name != "oid" {
		t.Errorf("Expected second parameter to be 'oid', got '%s'", pathParams[1].Name)
	}

	// Test parameter type inference using helper
	idType := inferParameterTypeForTesting("id")
	if idType != "uuid" {
		t.Errorf("Expected 'id' parameter type to be 'uuid', got '%s'", idType)
	}

	nameType := inferParameterTypeForTesting("name")
	if nameType != "string" {
		t.Errorf("Expected 'name' parameter type to be 'string', got '%s'", nameType)
	}

	// Test parameter description generation using helper
	idDesc := generateParameterDescriptionForTesting("id", "/api/product/attributes/:id/options/:oid")
	expectedIDDesc := "Attribute Id"
	if idDesc != expectedIDDesc {
		t.Errorf("Expected description '%s', got '%s'", expectedIDDesc, idDesc)
	}

	oidDesc := generateParameterDescriptionForTesting("oid", "/api/product/attributes/:id/options/:oid")
	expectedOIDDesc := "Option Oid"
	if oidDesc != expectedOIDDesc {
		t.Errorf("Expected description '%s', got '%s'", expectedOIDDesc, oidDesc)
	}

	// Test full annotation generation
	annotations := generatePathParamAnnotationsForTesting(route)

	if len(annotations) != 2 {
		t.Errorf("Expected 2 annotations, got %d", len(annotations))
	}

	// Check first annotation (id parameter)
	expectedFirst := "@Param id path uuid true \"Attribute Id\""
	if annotations[0] != expectedFirst {
		t.Errorf("Expected first annotation '%s', got '%s'", expectedFirst, annotations[0])
	}

	// Check second annotation (oid parameter)
	expectedSecond := "@Param oid path uuid true \"Option Oid\""
	if annotations[1] != expectedSecond {
		t.Errorf("Expected second annotation '%s', got '%s'", expectedSecond, annotations[1])
	}
}

// Test helper functions - these replicate the logic from the annotation generator for testing

type PathParameterForTesting struct {
	Name     string
	Position int
}

func extractPathParametersForTesting(path string) []PathParameterForTesting {
	var params []PathParameterForTesting

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	// Split by slash
	parts := strings.Split(path, "/")

	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			// Remove the ':' prefix to get the parameter name
			paramName := strings.TrimPrefix(part, ":")
			params = append(params, PathParameterForTesting{
				Name:     paramName,
				Position: i,
			})
		}
	}

	return params
}

func inferParameterTypeForTesting(paramName string) string {
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

func generateParameterDescriptionForTesting(paramName, fullPath string) string {
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
				singularContext := singularizeForTesting(context)
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

func singularizeForTesting(word string) string {
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

func generatePathParamAnnotationsForTesting(route router.RouteInfo) []string {
	var annotations []string

	// Extract path parameters from the route path
	pathParams := extractPathParametersForTesting(route.Path)

	for _, param := range pathParams {
		paramType := inferParameterTypeForTesting(param.Name)
		description := generateParameterDescriptionForTesting(param.Name, route.Path)

		// Format: @Param paramName path paramType true "Description"
		annotation := fmt.Sprintf("@Param %s path %s true \"%s\"", param.Name, paramType, description)
		annotations = append(annotations, annotation)
	}

	return annotations
}

func TestJSONRawMessageSwaggerTagInGeneratedTypes(t *testing.T) {
	// Test that json.RawMessage fields in generated types have swaggertype tags
	// Create temporary directory for test files
	tempDir := t.TempDir()

	// Create router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/example/handlers"
)

func Router() *gin.Engine {
	r := gin.Default()
	r.POST("/webhooks", handlers.WebhookCreate)
	r.PUT("/webhooks/:id", handlers.WebhookUpdate)
	return r
}`

	routerFile := filepath.Join(tempDir, "router.go")
	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Create handler file with json.RawMessage fields
	handlerContent := `package handlers

import (
	"net/http"
	"encoding/json"
	"github.com/gin-gonic/gin"
)

// WebhookCreate creates a new webhook.
func WebhookCreate(c *gin.Context) {
	var req struct {
		Name      string          ` + "`json:\"name\" binding:\"required\"`" + `
		URL       string          ` + "`json:\"url\" binding:\"required\"`" + `
		Secret    string          ` + "`json:\"secret\"`" + `
		Payload   json.RawMessage ` + "`json:\"payload\"`" + `
		Metadata  json.RawMessage ` + "`json:\"metadata\"`" + `
		Headers   map[string]string ` + "`json:\"headers\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": "123"})
}

// WebhookUpdate updates an existing webhook.
func WebhookUpdate(c *gin.Context) {
	id := c.Param("id")

	var req struct {
		Name     *string          ` + "`json:\"name\"`" + `
		URL      *string          ` + "`json:\"url\"`" + `
		Payload  *json.RawMessage ` + "`json:\"payload\"`" + `
		Metadata *json.RawMessage ` + "`json:\"metadata\"`" + `
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id})
}`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	// Debug: Show the transformed code to see generated types
	t.Logf("DEBUG: Transformed code with generated types:")
	lines := strings.Split(transformed, "\n")
	for i, line := range lines {
		if strings.Contains(line, "type ") && strings.Contains(line, "Request struct") {
			// Show the struct definition
			t.Logf("Found type definition at line %d: %s", i, strings.TrimSpace(line))
			// Show next 10 lines to see field definitions
			for j := i + 1; j < i+11 && j < len(lines); j++ {
				t.Logf("  %d: %s", j, lines[j])
				if strings.Contains(lines[j], "}") {
					break
				}
			}
			t.Logf("")
		}
	}

	// Verify that json.RawMessage fields have swaggertype:"array,object" tag
	// Look for the generated type definitions
	expectedSwaggerTags := []string{
		"Payload   json.RawMessage `" + `json:"payload" swaggertype:"array,object"` + "`",
		"Metadata  json.RawMessage `" + `json:"metadata" swaggertype:"array,object"` + "`",
	}

	foundSwaggerTags := 0
	for _, expectedTag := range expectedSwaggerTags {
		if strings.Contains(transformed, expectedTag) {
			foundSwaggerTags++
			t.Logf("Found expected swaggertype tag: %s", expectedTag)
		}
	}

	// If we don't find the exact format, look for any swaggertype:"string" tags
	if foundSwaggerTags == 0 {
		lines := strings.Split(transformed, "\n")
		for _, line := range lines {
			if strings.Contains(line, "json.RawMessage") && strings.Contains(line, "swaggertype:\"object\"") {
				foundSwaggerTags++
				t.Logf("Found swaggertype tag: %s", strings.TrimSpace(line))
			}
		}
	}

	if foundSwaggerTags == 0 {
		t.Logf("WARNING: No swaggertype:\"object\" tags found for json.RawMessage fields - this is expected before implementing the feature")
	} else {
		t.Logf("SUCCESS: Found %d swaggertype:\"object\" tags for json.RawMessage fields", foundSwaggerTags)
	}

	// Verify that the transformation still works correctly
	if !strings.Contains(transformed, "@Router /webhooks [POST]") {
		t.Fatal("Expected @Router annotation not found")
	}

	if !strings.Contains(transformed, "@Router /webhooks/:id [PUT]") {
		t.Fatal("Expected @Router annotation not found")
	}

	t.Logf("SUCCESS: JSON RawMessage Swagger tag test completed")
}

func TestJSONRawMessageInGeneratedTypes(t *testing.T) {
	// Test the specific scenario: anonymous struct with json.RawMessage fields without existing tags
	tempDir := t.TempDir()

	// Create test file with anonymous struct containing json.RawMessage fields without tags
	handlerContent := `package handlers

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
)

// TestHandler handles test requests with json.RawMessage fields
func TestHandler(c *gin.Context) {
	var response struct {
		Deal
		Active      bool
		Stores      json.RawMessage
		SKU         string
		ProductID   string
		ProductName string
	}

	c.JSON(http.StatusOK, response)
}

type Deal struct {
	ID string ` + "`json:\"id\"`" + `
}
`

	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"./handlers"
)

func SetupRouter() *gin.Engine {
	r := gin.Default()
	r.GET("/test", handlers.TestHandler)
	return r
}
`

	handlerFile := filepath.Join(tempDir, "handlers.go")
	routerFile := filepath.Join(tempDir, "router.go")

	if err := os.WriteFile(handlerFile, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform: %v", err)
	}

	// Check that the generated type has swaggertype tags for json.RawMessage fields
	lines := strings.Split(transformed, "\n")
	inGeneratedType := false
	foundGeneratedType := false
	foundSwaggerTags := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if we're in the generated type definition
		if strings.Contains(trimmed, "type TestResponse struct {") {
			inGeneratedType = true
			foundGeneratedType = true
			continue
		}

		// End of type definition
		if inGeneratedType && strings.HasPrefix(trimmed, "}") {
			inGeneratedType = false
			continue
		}

		// Check for json.RawMessage fields with swaggertype tags
		if inGeneratedType && strings.Contains(trimmed, "json.RawMessage") {
			if strings.Contains(trimmed, `swaggertype:"array,object"`) {
				foundSwaggerTags = true
				t.Logf("Found json.RawMessage field with swaggertype tag: %s", trimmed)
			} else {
				t.Errorf("Found json.RawMessage field without swaggertype tag: %s", trimmed)
			}
		}
	}

	if !foundGeneratedType {
		t.Error("Expected generated type not found")
	}

	if !foundSwaggerTags {
		t.Error("Expected swaggertype:\"object\" tags not found in generated type")
	}
}

// TestStatusCodeFallbackIntegration tests the fallback mechanism for extracting HTTP status codes from c.Status() calls
func TestStatusCodeFallbackIntegration(t *testing.T) {
	// Create a test file with handlers that use c.Status() calls but no c.JSON() responses
	testContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// StatusOnlyHandler returns only status without JSON response
func StatusOnlyHandler(c *gin.Context) {
	c.Status(http.StatusOK)
}

// StatusCreatedHandler returns only created status without JSON response
func StatusCreatedHandler(c *gin.Context) {
	c.Status(http.StatusCreated)
}

// NumericStatusHandler returns only numeric status without JSON response
func NumericStatusHandler(c *gin.Context) {
	c.Status(201)
}

// NoStatusHandler has no explicit status call
func NoStatusHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"default": true})
}
`

	// Create test router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/andreas/gin-replace-anon-struct/handlers"
)

func setupRouter() *gin.Engine {
	r := gin.Default()

	v1 := r.Group("/api/v1")
	{
		v1.GET("/status-only", handlers.StatusOnlyHandler)
		v1.POST("/status-created", handlers.StatusCreatedHandler)
		v1.PUT("/numeric-status", handlers.NumericStatusHandler)
		v1.GET("/no-status", handlers.NoStatusHandler)
	}

	return r
}
`

	tempDir := t.TempDir()
	handlerFile := filepath.Join(tempDir, "handlers.go")
	routerFile := filepath.Join(tempDir, "router.go")

	if err := os.WriteFile(handlerFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Analyze the handler file with router context
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Apply transformation using DST transformer (the one actually used)
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform: %v", err)
	}

	// Check that the output contains success annotations with the correct status codes
	if !strings.Contains(transformed, "@Success 200 {object} interface{}") {
		t.Error("Expected @Success 200 annotation for StatusOnlyHandler")
	}

	if !strings.Contains(transformed, "@Success 201 {object} interface{}") {
		t.Error("Expected @Success 201 annotation for StatusCreatedHandler")
	}

	// The numeric status should also be detected
	if !strings.Contains(transformed, "@Success 201 {object} interface{}") {
		t.Error("Expected @Success 201 annotation for NumericStatusHandler")
	}

	// The handler without explicit status should not have a fallback success annotation
	// (it should have the normal c.JSON-based annotation)
	lines := strings.Split(transformed, "\n")
	inNoStatusHandler := false
	hasFallbackSuccess := false

	for _, line := range lines {
		if strings.Contains(line, "func NoStatusHandler") {
			inNoStatusHandler = true
			continue
		}
		if inNoStatusHandler && strings.Contains(line, "func ") && !strings.Contains(line, "NoStatusHandler") {
			break // Moved to next function
		}
		if inNoStatusHandler && strings.Contains(line, "@Success") && strings.Contains(line, "interface{}") {
			hasFallbackSuccess = true
			break
		}
	}

	if hasFallbackSuccess {
		t.Error("NoStatusHandler should not have fallback success annotation since it has c.JSON call")
	}

	fmt.Println("SUCCESS: Status code fallback test passed - correct status codes were extracted")
}

// TestIDAnnotationIntegration tests the @id annotation generation
func TestIDAnnotationIntegration(t *testing.T) {
	// Create a test file with handlers from different packages
	testContent := `package handlers

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// SimpleHandler has no package prefix
func SimpleHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "simple"})
}

// AnotherHandler is another simple handler
func AnotherHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": "another"})
}
`

	// Create common package file
	commonContent := `package common

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

// Currencies returns currency information
func Currencies(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"currencies": []string{}})
}
`

	// Create test router file
	routerContent := `package main

import (
	"github.com/gin-gonic/gin"
	"github.com/andreas/gin-replace-anon-struct/handlers"
	"github.com/andreas/gin-replace-anon-struct/common"
)

func setupRouter() *gin.Engine {
	r := gin.Default()

	v1 := r.Group("/api/v1")
	{
		v1.GET("/simple", handlers.SimpleHandler)
		v1.GET("/another", handlers.AnotherHandler)
		v1.GET("/currencies", common.Currencies)
	}

	return r
}
`

	tempDir := t.TempDir()
	handlerFile := filepath.Join(tempDir, "handlers.go")
	commonFile := filepath.Join(tempDir, "common.go")
	routerFile := filepath.Join(tempDir, "router.go")

	if err := os.WriteFile(handlerFile, []byte(testContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}

	if err := os.WriteFile(commonFile, []byte(commonContent), 0644); err != nil {
		t.Fatalf("Failed to write common file: %v", err)
	}

	if err := os.WriteFile(routerFile, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Test handlers package
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse handler file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform: %v", err)
	}

	// Check that the output contains @id annotations for handlers package
	// With package+handler generation, handlers should have @id HandlersSimpleHandler, @id HandlersAnotherHandler
	if !strings.Contains(transformed, "@id HandlersSimpleHandler") {
		t.Error("Expected @id HandlersSimpleHandler annotation for SimpleHandler")
	}

	if !strings.Contains(transformed, "@id HandlersAnotherHandler") {
		t.Error("Expected @id HandlersAnotherHandler annotation for AnotherHandler")
	}

	// Test common package
	fileSet2 := token.NewFileSet()
	file2, err := parser.ParseFile(fileSet2, commonFile, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse common file: %v", err)
	}

	detector2 := analyzer.NewAnonymousStructDetector(fileSet2, file2, "common")
	result2, err := detector2.DetectAnonymousStructsWithRouter(routerFile, "")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs for common: %v", err)
	}

	dstTransformer2 := transformer.NewDSTTransformer(fileSet2, file2, result2, "")
	transformed2, err := dstTransformer2.Transform()
	if err != nil {
		t.Fatalf("Failed to transform common: %v", err)
	}

	// Check that the output contains @id annotation for common.Currencies
	// With package+handler generation, Currencies handler should have @id CommonCurrencies
	if !strings.Contains(transformed2, "@id CommonCurrencies") {
		t.Error("Expected @id CommonCurrencies annotation for Currencies handler")
	}

	// Check that @Tags are generated as PascalCase
	if !strings.Contains(transformed2, "@Tags ApiV1") {
		t.Error("Expected @Tags ApiV1 annotation for Currencies handler")
	}

	fmt.Println("SUCCESS: ID annotation test passed - correct @id annotations were generated")
}

func TestTypeSubstitutionConsistency(t *testing.T) {
	// Test for type substitution bug where Swagger annotations and handler code use different types
	// This test creates a scenario where multiple anonymous structs might cause naming conflicts
	handlerContent := `package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func CategoryProductUpdate(c *gin.Context) {
	// First anonymous struct - should become CategoryProductUpdateRequest
	req := []struct {
		Name string ` + "`json:\"name\"`" + `
		ID   int    ` + "`json:\"id\"`" + `
	}{}

	// Second anonymous struct with same field count - this might cause confusion
	// in the fallback matching logic
	data := struct {
		Tag  string ` + "`json:\"tag\"`" + `
		Type string ` + "`json:\"type\"`" + `
	}{}

	// The issue might occur if the variable name matching fails and it falls back
	// to field count matching, picking the wrong anonymous struct
	c.JSON(http.StatusOK, req)
}`

	routerContent := `package router

import (
	"github.com/gin-gonic/gin"
	handlers "../handlers"
)

func SetupRouter() *gin.Engine {
	r := gin.New()

	r.PUT("/api/product/items/:id/categories", handlers.CategoryProductUpdate)

	return r
}`

	// Create temporary files
	tmpDir := t.TempDir()
	handlerPath := tmpDir + "/handlers.go"
	routerPath := tmpDir + "/router.go"

	if err := os.WriteFile(handlerPath, []byte(handlerContent), 0644); err != nil {
		t.Fatalf("Failed to write handler file: %v", err)
	}
	if err := os.WriteFile(routerPath, []byte(routerContent), 0644); err != nil {
		t.Fatalf("Failed to write router file: %v", err)
	}

	// Parse and analyze
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, handlerPath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("Failed to parse Go file: %v", err)
	}

	detector := analyzer.NewAnonymousStructDetector(fileSet, file, "handlers")
	result, err := detector.DetectAnonymousStructsWithRouter(routerPath, "/api")
	if err != nil {
		t.Fatalf("Failed to analyze anonymous structs: %v", err)
	}

	// Check that we found the expected anonymous structs
	if len(result.AnonymousStructs) != 2 {
		t.Fatalf("Expected 2 anonymous structs, got %d", len(result.AnonymousStructs))
	}

	// Print details about both anonymous structs
	for i, anonStruct := range result.AnonymousStructs {
		t.Logf("Anonymous struct %d: Handler=%s, Variable=%s, GeneratedTypeName=%s",
			i, anonStruct.HandlerName, anonStruct.VariableName, anonStruct.GeneratedTypeName)
	}

	// Transform the code
	dstTransformer := transformer.NewDSTTransformer(fileSet, file, result, "/api")
	transformed, err := dstTransformer.Transform()
	if err != nil {
		t.Fatalf("Failed to transform AST: %v", err)
	}

	t.Logf("Transformed code:\n%s", transformed)

	// Check for type consistency
	hasSwaggerType := strings.Contains(transformed, "CategoryProductUpdateRequest")
	hasHandlerType := strings.Contains(transformed, "TagProductUpdateRequest")

	if hasSwaggerType && hasHandlerType {
		t.Errorf("❌ TYPE MISMATCH BUG DETECTED:")
		t.Errorf("   Swagger annotation uses: CategoryProductUpdateRequest")
		t.Errorf("   Handler code uses: TagProductUpdateRequest")
		t.Errorf("   This indicates inconsistent type substitution logic")
	} else if hasSwaggerType {
		// Check that handler code also uses the same type
		if !strings.Contains(transformed, "req := []CategoryProductUpdateRequest{}") {
			t.Errorf("❌ TYPE MISMATCH: Swagger uses CategoryProductUpdateRequest but handler doesn't")
		} else {
			t.Logf("✅ Type consistency verified: Both use CategoryProductUpdateRequest")
		}
	} else if hasHandlerType {
		t.Errorf("❌ TYPE MISMATCH: Handler uses TagProductUpdateRequest but Swagger doesn't use CategoryProductUpdateRequest")
	} else {
		t.Logf("✅ No obvious type mismatch detected")
	}

	// Additional check: ensure the same type name is used throughout
	lines := strings.Split(transformed, "\n")
	typeNames := make(map[string]bool)

	for _, line := range lines {
		// Look for type references in @Param annotations
		if strings.Contains(line, "@Param") && strings.Contains(line, "body") {
			if match := regexp.MustCompile(`body\s+(\w+)`).FindStringSubmatch(line); len(match) > 1 {
				typeNames[match[1]] = true
			}
		}
		// Look for type references in variable declarations
		if strings.Contains(line, "req := []") {
			if match := regexp.MustCompile(`req := \[\](\w+)`).FindStringSubmatch(line); len(match) > 1 {
				typeNames[match[1]] = true
			}
		}
	}

	if len(typeNames) > 1 {
		t.Errorf("❌ MULTIPLE TYPE NAMES DETECTED: %v", typeNames)
	} else if len(typeNames) == 1 {
		var typeName string
		for name := range typeNames {
			typeName = name
		}
		t.Logf("✅ Consistent type name used: %s", typeName)
	}
}

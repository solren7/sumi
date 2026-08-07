package apps

import (
	"strings"
	"time"

	"sumi/config"
	docs "sumi/docs"
	"sumi/internal/handlers"
	"sumi/middleware"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
)

func RegisterRoutes(app *fiber.App, handler *handlers.Handler, cfg *config.Config) {
	// Middlewares
	allowedOrigins := strings.Split(cfg.AllowedOrigins, ",")
	app.Use(cors.New(cors.Config{
		AllowOrigins: allowedOrigins,
	}))
	app.Use(middleware.LogxMeta)
	app.Use(middleware.RequestLog())
	app.Get("/openapi.yaml", func(c fiber.Ctx) error {
		return c.SendFile("./docs/openapi.yaml")
	})
	app.Get("/swagger/doc.json", func(c fiber.Ctx) error {
		c.Type("json", "utf-8")
		return c.SendString(docs.SwaggerInfo.ReadDoc())
	})
	app.Get("/swagger", func(c fiber.Ctx) error {
		return c.Redirect().Status(fiber.StatusMovedPermanently).To("/swagger/index.html")
	})
	app.Get("/swagger/index.html", func(c fiber.Ctx) error {
		c.Type("html", "utf-8")
		return c.SendString(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Sumi API Swagger</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function () {
      SwaggerUIBundle({
        url: "/swagger/doc.json",
        dom_id: "#swagger-ui",
        deepLinking: true,
        persistAuthorization: true
      });
    };
  </script>
</body>
</html>`)
	})

	// Routes
	api := app.Group("/api")
	api.Get("/ping", handler.Ping)

	// Rate limiter for auth endpoints: 10 requests per minute per IP.
	authLimiter := limiter.New(limiter.Config{
		Max:        10,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c fiber.Ctx) error {
			return fiber.NewError(fiber.StatusTooManyRequests, "Too many requests, please try again later")
		},
	})

	// Auth Routes
	auth := api.Group("/auth")
	auth.Post("/check-email", authLimiter, handler.CheckEmail)
	auth.Post("/register", authLimiter, handler.Register)
	auth.Post("/login", authLimiter, handler.Login)
	auth.Post("/refresh", authLimiter, handler.Refresh)
	auth.Post("/logout", handler.Logout)
	auth.Get("/me", middleware.JWTOnly(handler.S.Auth), handler.Me)

	apiKeys := api.Group("/api-keys", middleware.JWTOnly(handler.S.Auth))
	apiKeys.Post("/", handler.CreateAPIKey)
	apiKeys.Get("/", handler.ListAPIKeys)
	apiKeys.Post("/:id/revoke", handler.RevokeAPIKey)
	apiKeys.Delete("/:id", handler.DeleteAPIKey)

	categories := api.Group("/categories", middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "categories:read"))
	categories.Get("/", handler.ListCategories)

	authenticatedBills := middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "transactions:read")
	writeBills := middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "transactions:write")
	updateBills := middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "transactions:update")
	deleteBills := middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "transactions:delete")

	// Bills Routes
	bills := api.Group("/bills")
	bills.Get("/", authenticatedBills, handler.ListBills)
	bills.Get("/:id", authenticatedBills, handler.GetBill)
	bills.Post("/", writeBills, handler.CreateBill)
	bills.Put("/:id", updateBills, handler.UpdateBill)
	bills.Delete("/:id", deleteBills, handler.DeleteBill)

	transactions := api.Group("/transactions")
	transactions.Get("/", authenticatedBills, handler.ListBills)
	transactions.Get("/:id", authenticatedBills, handler.GetBill)
	transactions.Post("/", writeBills, handler.CreateBill)
	transactions.Put("/:id", updateBills, handler.UpdateBill)
	transactions.Delete("/:id", deleteBills, handler.DeleteBill)

	stats := api.Group("/stats", middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "stats:read"))
	stats.Get("/monthly", handler.GetMonthlyStats)
	stats.Get("/daily", handler.GetDailyStats)
	stats.Get("/category", handler.GetCategoryStats)

	// Backward-compatible alias for the original home stats endpoint.
	homeStats := api.Group("/stats")
	homeStats.Get("/home", middleware.JWTOrAPIKey(handler.S.Auth, handler.S.APIKey, "stats:read"), handler.GetMonthlyStats)
}

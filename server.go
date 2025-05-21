package dziproxylib

import (
	"github.com/rs/cors"
	"net/http"
)

var memCache *InMemoryCache

const (
	defaultInMemoryCacheCapacityItems = 1000
	defaultInMemoryCacheCapacityBytes = 50 * 1024 * 1024 // 50MB
)

func DziProxyServer(config *Config) (*http.Server, error) {
	LibConfig = config

	// Set default for CleanupTimeout if not provided
	if LibConfig.CleanupTimeout == 0 {
		LibConfig.CleanupTimeout = 10 * time.Minute // Default to 10 minutes
	}

	// Initialize in-memory cache
	items := LibConfig.InMemoryCacheCapacityItems
	if items <= 0 {
		items = defaultInMemoryCacheCapacityItems
	}
	bytes := LibConfig.InMemoryCacheCapacityBytes
	if bytes <= 0 {
		bytes = defaultInMemoryCacheCapacityBytes
	}
	memCache = NewInMemoryCache(items, bytes)

	mux := http.NewServeMux()
	//mux.HandleFunc("/heat/{path...}", heatHandler)
	mux.HandleFunc("/{path...}", handler)
	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"}, // Разрешить все домены
		AllowedMethods: []string{
			http.MethodHead,
			http.MethodGet,
			http.MethodPost,
			http.MethodPut,
			http.MethodPatch,
			http.MethodDelete,
			http.MethodOptions, // Разрешить preflight-запросы
		},
		AllowedHeaders:      []string{"*"}, // Разрешить любые заголовки
		ExposedHeaders:      []string{"*"}, // Разрешить клиенту доступ к этим заголовкам
		AllowCredentials:    true,          // Разрешить отправку cookies
		AllowPrivateNetwork: true,          // Разрешить запросы в локальную сеть
	})
	h := c.Handler(mux)
	server := &http.Server{
		Addr:    LibConfig.Listen,
		Handler: h,
	}

	return server, nil
}

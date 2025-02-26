package dziproxylib

import (
	"github.com/rs/cors"
	"net/http"
)

func DziProxyServer(config *Config) (*http.Server, error) {
	LibConfig = config

	mux := http.NewServeMux()
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

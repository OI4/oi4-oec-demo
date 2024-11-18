package weather

import (
	"encoding/json"
	"go.uber.org/zap"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWeather(t *testing.T) {
	// Mock response data
	mockResponse := ServiceResponse{
		Coord: Coordinates{Lon: 10.0, Lat: 50.0},
		Weather: []Weather{
			{ID: 800, Main: "Clear", Description: "clear sky", Icon: "01d"},
		},
		Base:       "stations",
		Main:       MainWeather{Temp: 293.15, FeelsLike: 293.15, Pressure: 1013, Humidity: 53},
		Visibility: 10000,
		Wind:       Wind{Speed: 1.5, Deg: 350},
		Clouds:     Clouds{All: 1},
		Sys:        System{Country: "DE", Sunrise: 1600416000, Sunset: 1600461600},
		Timezone:   7200,
		ID:         123456,
		Name:       "Freiburg",
		Cod:        200,
	}

	// Create a test server
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(mockResponse)

		if err != nil {
			return
		}
	}))
	defer testServer.Close()

	ws := NewServiceWithURL(testServer.URL, "test_app_id", Metric, getLogger())
	coordinates := Coordinates{Lon: 10.0, Lat: 50.0}
	weather, err := ws.GetWeather(coordinates, "en")

	require.NoError(t, err)
	assert.Equal(t, mockResponse, *weather)
}

func getLogger() *zap.SugaredLogger {
	logger, _ := zap.NewProduction()
	defer func(logger *zap.Logger) {
		err := logger.Sync()
		if err != nil {
			log.Println("Error syncing logger:", err)
		}
	}(logger) // flushes buffer, if any

	return logger.Sugar()
}

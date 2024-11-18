package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"io"
	"net/http"
	"strings"
	"time"
)

var errQueryFailed = errors.New("failed to query OpenWeatherMap API")

const DefaultBaseURL = "https://api.openweathermap.org/data/2.5/weather"
const timeout = 10 * time.Second

type Service struct {
	BaseURL string
	AppID   string
	Units   Unit
	logger  *zap.SugaredLogger
}

func NewService(appid string, unit Unit, logger *zap.SugaredLogger) *Service {
	return NewServiceWithURL(DefaultBaseURL, appid, unit, logger)
}

func NewServiceWithURL(url string, appid string, units Unit, logger *zap.SugaredLogger) *Service {
	appidTrimmed := strings.TrimSpace(appid)

	return &Service{BaseURL: url, AppID: appidTrimmed, Units: units, logger: logger}
}

func (ws *Service) GetWeather(
	coordinates Coordinates,
	language string,
) (*ServiceResponse, error) {
	url := fmt.Sprintf(
		"%s?lat=%v&lon=%v&appid=%s&units=%s&lang=%s",
		ws.BaseURL,
		coordinates.Lat,
		coordinates.Lon,
		ws.AppID,
		ws.Units,
		language)

	client := &http.Client{Timeout: timeout}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)

	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	resp, err := client.Do(request) //nolint:bodyclose

	defer func(Body io.ReadCloser) {
		err = Body.Close()
		if err != nil {
			ws.logger.Warn("Failed to close response body")
		}
	}(resp.Body)

	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s", errQueryFailed, resp.Status)
	}

	var weatherResponse ServiceResponse

	if err = json.NewDecoder(resp.Body).Decode(&weatherResponse); err != nil {
		return nil, err //nolint:wrapcheck
	}

	return &weatherResponse, nil
}

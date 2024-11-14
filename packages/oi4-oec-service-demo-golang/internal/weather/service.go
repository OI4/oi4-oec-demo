package weather

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultBaseUrl = "https://api.openweathermap.org/data/2.5/weather"

type Service struct {
	BaseUrl string
	AppID   string
	Units   Unit
}

func NewService(appid string, unit Unit) *Service {
	return NewServiceWithUrl(DefaultBaseUrl, appid, unit)
}

func NewServiceWithUrl(url string, appid string, units Unit) *Service {
	appidTrimmed := strings.TrimSpace(appid)
	return &Service{BaseUrl: url, AppID: appidTrimmed, Units: units}
}

func (ws *Service) GetWeather(coordinates Coordinates, language string) (*ServiceResponse, error) {
	url := fmt.Sprintf("%s?lat=%v&lon=%v&appid=%s&units=%s&lang=%s", ws.BaseUrl, coordinates.Lat, coordinates.Lon, ws.AppID, ws.Units, language)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			fmt.Println("Failed to close response body")
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to query OpenWeatherMap API: %s", resp.Status)
	}

	var weatherResponse ServiceResponse
	if err := json.NewDecoder(resp.Body).Decode(&weatherResponse); err != nil {
		return nil, err
	}

	return &weatherResponse, nil
}

func main() {
	ws := NewService("your_app_id", "metric")
	coordinates := Coordinates{Lon: 10.0, Lat: 50.0}
	weather, err := ws.GetWeather(coordinates, "en")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Printf("Weather: %+v\n", weather)
}

package weather

type Unit string

const (
	Metric   Unit = "metric"
	Standard Unit = "standard"
	Imperial Unit = "imperial"
)

type ServiceResponse struct {
	Coord      Coordinates `json:"coord"`
	Weather    []Weather   `json:"weather"`
	Base       string      `json:"base"`
	Main       MainWeather `json:"main"`
	Visibility int         `json:"visibility"`
	Wind       Wind        `json:"wind"`
	Clouds     Clouds      `json:"clouds"`
	Rain       Rain        `json:"rain"`
	Snow       Snow        `json:"snow"`
	Sys        System      `json:"sys"`
	Timezone   int         `json:"timezone"`
	ID         int64       `json:"id"`
	Name       string      `json:"name"`
	Cod        int         `json:"cod"`
}

type Coordinates struct {
	Lon float32 `json:"lon"`
	Lat float32 `json:"lat"`
}

type Weather struct {
	ID          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type MainWeather struct {
	Temp      float64 `json:"temp"`
	FeelsLike float64 `json:"feels_like"`
	Pressure  int     `json:"pressure"`
	Humidity  int     `json:"humidity"`
	TempMin   float64 `json:"temp_min"`
	TempMax   float64 `json:"temp_max"`
	SeaLevel  int     `json:"sea_level"`
	GrndLevel int     `json:"grnd_level"`
}

type Wind struct {
	Speed float64 `json:"speed"`
	Deg   int     `json:"deg"`
	Gust  float64 `json:"gust"`
}

type Clouds struct {
	All int `json:"all"`
}

type Rain struct {
	OneH   float64 `json:"1h"`
	ThreeH float64 `json:"3h"`
}

type Snow struct {
	OneH   float64 `json:"1h"`
	ThreeH float64 `json:"3h"`
}

type System struct {
	Type    int     `json:"type"`
	ID      int     `json:"id"`
	Message float64 `json:"message"`
	Country string  `json:"country"`
	Sunrise int     `json:"sunrise"`
	Sunset  int     `json:"sunset"`
}

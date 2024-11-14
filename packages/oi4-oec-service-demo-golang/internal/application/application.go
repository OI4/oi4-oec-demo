package application

import (
	"fmt"
	"github.com/OI4/oi4-oec-demo/internal/weather"
	"github.com/OI4/oi4-oec-service-go/service/api"
	"github.com/OI4/oi4-oec-service-go/service/application"
	"github.com/OI4/oi4-oec-service-go/service/application/publication"
	"github.com/OI4/oi4-oec-service-go/service/application/source"
	"github.com/OI4/oi4-oec-service-go/service/container"
	"go.uber.org/zap"
	"time"
)

type WeatherApplication struct {
	*application.Oi4ApplicationImpl
	applicationSource *source.ApplicationSourceImpl
	mam               api.MasterAssetModel
	assets            map[string]AssetsEntry
	storage           *container.Storage
	weatherService    *weather.Service
	logger            *zap.SugaredLogger
}

func NewWeatherApplication(
	mam api.MasterAssetModel,
	storage *container.Storage,
	weatherService *weather.Service,
	logger *zap.SugaredLogger,
) *WeatherApplication {
	applicationSource := source.NewApplicationSourceImpl(mam)
	oi4Application, err := application.CreateNewApplication(api.ServiceTypeOTConnector, applicationSource, logger)
	if err != nil {
		logger.Fatal("Failed to create application:", err)
		panic(err)
	}

	assets := make(map[string]AssetsEntry)

	return &WeatherApplication{
		oi4Application,
		applicationSource,
		mam,
		assets,
		storage,
		weatherService,
		logger,
	}
}

func (app *WeatherApplication) AddAssets(assetList []Asset) {
	for _, asset := range assetList {
		app.AddAsset(asset)
	}
}

func (app *WeatherApplication) AddAsset(asset Asset) {
	key := asset.ToOi4Identifier().ToString()

	assetSource := source.NewAssetSourceImpl(asset.MasterAssetModel)
	oi4Asset := application.CreateNewAsset(assetSource, app.Oi4ApplicationImpl)
	dataAssetPublication := publication.NewResourcePublicationWithFilter(app.Oi4ApplicationImpl, assetSource, api.ResourceData, api.NewStringFilter("Oi4Data"))
	err := oi4Asset.RegisterPublication(dataAssetPublication)
	if err != nil {
		app.logger.Error("Failed to register publication:", err)
	}
	metaDataAssetPublication := publication.NewResourcePublication(app.Oi4ApplicationImpl, assetSource, api.ResourceMetadata)
	err = oi4Asset.RegisterPublication(metaDataAssetPublication)
	if err != nil {
		app.logger.Error("Failed to register publication:", err)
	}
	app.RegisterAsset(oi4Asset)

	assetEntry := AssetsEntry{
		assetSource,
		oi4Asset,
		asset,
	}

	app.assets[key] = assetEntry
}

func (app *WeatherApplication) Start(storage container.Storage) error {
	err := app.Oi4ApplicationImpl.Start(storage)
	if err != nil {
		return err
	}
	assetTicker := time.NewTicker(1 * time.Minute)
	go func() {
		for {
			<-assetTicker.C
			for key, asset := range app.assets {
				fmt.Println(key)
				fmt.Println(asset)
				lat := asset.asset.Location.Latitude
				lon := asset.asset.Location.Longitude
				response, rErr := app.weatherService.GetWeather(weather.Coordinates{Lon: lon, Lat: lat}, "en")
				if rErr != nil {
					app.logger.Warn("Failed to get weather data:", rErr)
					continue
				}
				data := api.NewOi4Data(response.Main.Temp)

				addValue := func(key string, value any) {
					dErr := data.AddSecondaryData(key, &value)

					if dErr != nil {
						app.logger.Warn("Failed to add secondary data:", dErr)
					}
				}

				addValue("Sv1", response.Main.Pressure)
				addValue("Sv2", response.Main.Humidity)

				asset.assetSource.UpdateData(data, "Oi4Data")
			}
		}
	}()
	return nil
}

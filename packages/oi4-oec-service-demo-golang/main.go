package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/OI4/oi4-oec-demo/internal/application"
	"github.com/OI4/oi4-oec-demo/internal/weather"
	"github.com/OI4/oi4-oec-service-go/service/api"
	"github.com/OI4/oi4-oec-service-go/service/container"
	"go.uber.org/zap"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	logger := getLogger()

	storage, mam, err := getStorage(logger)
	if err != nil {
		wd, _ := os.Getwd()
		logger.Info("Working directory:", wd)
		logger.Fatal("Failed to retrieve storage configuration:", err)
		panic(err)
	}

	appId, err := getAppId(storage)
	if err != nil {
		wd, _ := os.Getwd()
		logger.Info("Working directory:", wd)
		logger.Fatal("Failed to retrieve weather app id:", err)
		panic(err)
	}

	weatherService := weather.NewService(*appId, weather.Metric)

	assets, err := getAssets(storage.ApplicationSpecificStorages, logger)
	if err != nil {
		logger.Fatal("Failed to get assets:", err)
		panic(err)
	}

	app := application.NewWeatherApplication(*mam, storage, weatherService, logger)
	app.AddAssets(assets)

	if err = app.Start(*storage); err != nil {
		panic(err)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	app.Stop()

	os.Exit(0)
}

func getStorage(logger *zap.SugaredLogger) (*container.Storage, *api.MasterAssetModel, error) {
	baseDir, runtime := getEnvironment()

	logger.Info("Using app as: %s with base dir: %s\n", runtime, baseDir)

	isContainer := runtime == "container"

	var config container.StorageConfiguration
	var mam *api.MasterAssetModel
	var err error
	if isContainer {
		config = *container.DefaultStorageConfiguration()
		mam, err = getMasterAssetModel(container.DefaultOi4Folder)
		if err != nil {
			return nil, nil, err
		}
	} else {
		mam, err = getMasterAssetModel(filepath.Join(baseDir, container.DefaultOi4Folder))
		if err != nil {
			return nil, nil, err
		}
		config = container.StorageConfiguration{
			ContainerName:                        mam.SerialNumber,
			MessageBusStoragePath:                filepath.Join(baseDir, container.DefaultMessageBusStorageSubFolder),
			Oi4CertificateStoragePath:            filepath.Join(baseDir, container.DefaultOi4CertificateStorageSubFolder),
			SecretStoragePath:                    filepath.Join(baseDir, container.DefaultSecretsFolder),
			ApplicationSpecificConfigurationPath: filepath.Join(baseDir, container.DefaultApplicationSpecificConfigurationFolder),
			ApplicationSpecificDataPath:          filepath.Join(baseDir, container.DefaultApplicationSpecificDataFolder),
		}
	}

	storage, err := container.NewContainerStorage(config, logger)
	if err != nil {
		return nil, nil, err
	}

	return storage, mam, nil
}

func getMasterAssetModel(oi4Dir string) (*api.MasterAssetModel, error) {
	mamFile := filepath.Join(oi4Dir, "config", "mam.json")
	fileBytes, err := os.ReadFile(mamFile)
	if err != nil {
		return nil, &api.Error{
			Message: "Failed to read master asset model file from: " + mamFile,
			Err:     err,
		}
	}
	var mam api.MasterAssetModel
	err = json.Unmarshal(fileBytes, &mam)
	if err != nil {
		return nil, &api.Error{
			Message: "Failed to unmarshal master asset model file from: " + mamFile,
			Err:     err,
		}
	}

	return &mam, nil
}

func getEnvironment() (string, string) {
	baseDir, hasBaseDirEnv := os.LookupEnv("BASE_DIR")
	if !hasBaseDirEnv {
		baseDir = *flag.String("base", "", "base dir of the configuration")
	}

	runtime, hasRuntimeEnv := os.LookupEnv("RUNTIME")
	if !hasRuntimeEnv {
		runtime = *flag.String("runtime", "container", "runtime environment (program, container)")
	}

	if !hasBaseDirEnv || !hasRuntimeEnv {
		flag.Parse()
	}

	return baseDir, runtime
}

func getAppId(configuration *container.Storage) (*string, error) {
	file := filepath.Join(*configuration.SecretStorage.FolderPath, "weather_app_id")
	fileBytes, err := os.ReadFile(file)
	if err != nil {
		return nil, &api.Error{
			Message: "Failed to read weather app id file from: " + file,
			Err:     err,
		}
	}
	id := string(fileBytes)
	return &id, nil
}

func getAssets(appStorage *container.ApplicationSpecificStorages, logger *zap.SugaredLogger) ([]application.Asset, error) {
	folder := filepath.Join(appStorage.ConfigurationPath, "assets")

	files, err := os.ReadDir(folder)
	if err != nil {
		return nil, &api.Error{
			Message: "failed to read directory",
			Err:     err,
		}
	}

	var assets = make([]application.Asset, 0)
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		jsonBytes, fErr := readFile(filepath.Join(folder, file.Name()), logger)
		if fErr != nil {
			fmt.Println("Failed to asset read file: "+file.Name(), fErr)
		}

		var asset application.Asset
		err = json.Unmarshal(jsonBytes, &asset)
		if err != nil {
			logger.Error("Failed to unmarshal asset: "+file.Name(), err)
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func readFile(filePath string, logger *zap.SugaredLogger) ([]byte, error) {
	// Open the file
	file, err := os.Open(filePath)
	defer func(file *os.File) {
		err = file.Close()
		if err != nil {
			// TODO log as debug
			logger.Warnf("Error closing file: "+filePath, err)
		}
	}(file)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	_, err = io.Copy(&buffer, file)
	return buffer.Bytes(), err
}

func getLogger() *zap.SugaredLogger {
	logger, _ := zap.NewProduction()
	defer func(logger *zap.Logger) {
		err := logger.Sync()
		if err != nil {
			log.Print("Error flushing logger buffer: ", err)
		}
	}(logger) // flushes buffer, if any

	return logger.Sugar()
}

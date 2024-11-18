package application

import (
	"github.com/OI4/oi4-oec-service-go/service/api"
	"github.com/OI4/oi4-oec-service-go/service/application/source"
)

type AssetsEntry struct {
	assetSource *source.AssetSourceImpl
	oi4Asset    api.Asset
	asset       Asset
}

type Asset struct {
	api.MasterAssetModel
	Location Location `json:"location"`
}

type Location struct {
	Longitude float32 `json:"longitude"`
	Latitude  float32 `json:"latitude"`
}

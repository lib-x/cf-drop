package dropclient

type workerMetadataSpec struct {
	CompatibilityDate string           `json:"compatibility_date"`
	Assets            workerAssetsSpec `json:"assets"`
	Bindings          []workerBinding  `json:"bindings"`
}

type workerAssetsSpec struct {
	JWT    string             `json:"jwt"`
	Config workerAssetsConfig `json:"config"`
}

type workerAssetsConfig struct {
	NotFoundHandling string `json:"not_found_handling"`
}

type workerBinding struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func workerMetadata(jwt, compatibilityDate string) workerMetadataSpec {
	return workerMetadataSpec{
		CompatibilityDate: defaultString(compatibilityDate, DefaultCompatibilityDate),
		Assets: workerAssetsSpec{
			JWT: jwt,
			Config: workerAssetsConfig{
				NotFoundHandling: "single-page-application",
			},
		},
		Bindings: []workerBinding{
			{Name: "ASSETS", Type: "assets"},
		},
	}
}

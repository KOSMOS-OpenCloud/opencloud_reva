package defaults

import (
	"log"
	"os"
	"path"
)

var (
	// switch between modes
	BaseDataPathType = "homedir" // or "path"
	// default data path
	BaseDataPathValue = "/var/lib/opencloud"
)

func BaseDataPath() string {

	// It is not nice to have hidden / secrete configuration options
	// But how can we update the base path for every occurrence with a flagset option?
	// This is currently not possible and needs a new configuration concept
	p := os.Getenv("OC_BASE_DATA_PATH")
	if p != "" {
		return p
	}

	switch BaseDataPathType {
	case "homedir":
		dir, err := os.UserHomeDir()
		if err != nil {
			// fallback to BaseDatapathValue for users without home
			return BaseDataPathValue
		}
		return path.Join(dir, ".opencloud")
	case "path":
		return BaseDataPathValue
	default:
		log.Fatalf("BaseDataPathType %s not found", BaseDataPathType)
		return ""
	}
}

var (
	// switch between modes
	BaseConfigPathType = "homedir" // or "path"
	// default config path
	BaseConfigPathValue = "/etc/opencloud"
)

func BaseConfigPath() string {

	// It is not nice to have hidden / secrete configuration options
	// But how can we update the base path for every occurrence with a flagset option?
	// This is currently not possible and needs a new configuration concept
	p := os.Getenv("OC_CONFIG_DIR")
	if p != "" {
		return p
	}

	switch BaseConfigPathType {
	case "homedir":
		dir, err := os.UserHomeDir()
		if err != nil {
			// fallback to BaseConfigPathValue for users without home
			return BaseConfigPathValue
		}
		return path.Join(dir, ".opencloud", "config")
	case "path":
		return BaseConfigPathValue
	default:
		log.Fatalf("BaseConfigPathType %s not found", BaseConfigPathType)
		return ""
	}
}

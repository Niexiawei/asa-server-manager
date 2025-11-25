package app

import (
	"embed"
)

//go:embed dist
var embeddedDist embed.FS

func GetDistFs() embed.FS {
	return embeddedDist
}

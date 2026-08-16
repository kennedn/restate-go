package ms600

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// info.json is a checked-in copy of github.com/tom-code/gomat/symbols/info.json.
// Keeping it local allows go:embed to include the schema in production binaries;
// arbitrary data files from imported modules are not embedded automatically.
//
//go:embed info.json
var matterSchemaJSON []byte

type schemaCluster struct {
	Name       string
	Attributes map[uint32]string
	Commands   map[uint32]string
}

type schemaCatalog struct {
	Clusters map[string]schemaJSONCluster `json:"Clusters"`
}

type schemaJSONCluster struct {
	Name       string           `json:"Name"`
	ID         uint32           `json:"Id"`
	Attributes []schemaJSONItem `json:"Attributes"`
	Commands   []schemaJSONItem `json:"Commands"`
}

type schemaJSONItem struct {
	Name string `json:"Name"`
	ID   uint32 `json:"Id"`
}

var matterSchema = mustLoadMatterSchema()

func mustLoadMatterSchema() map[uint32]schemaCluster {
	var catalog schemaCatalog
	if err := json.Unmarshal(matterSchemaJSON, &catalog); err != nil {
		panic(fmt.Sprintf("parse embedded gomat Matter schema: %v", err))
	}

	clusters := make(map[uint32]schemaCluster, len(catalog.Clusters))
	for _, source := range catalog.Clusters {
		cluster := schemaCluster{
			Name:       source.Name,
			Attributes: make(map[uint32]string, len(source.Attributes)),
			Commands:   make(map[uint32]string, len(source.Commands)),
		}
		for _, attribute := range source.Attributes {
			// Some upstream entries are duplicated. Preserve the first name so
			// loading remains deterministic and matches the former generator.
			if _, exists := cluster.Attributes[attribute.ID]; !exists {
				cluster.Attributes[attribute.ID] = attribute.Name
			}
		}
		for _, command := range source.Commands {
			if _, exists := cluster.Commands[command.ID]; !exists {
				cluster.Commands[command.ID] = command.Name
			}
		}
		clusters[source.ID] = cluster
	}
	return clusters
}

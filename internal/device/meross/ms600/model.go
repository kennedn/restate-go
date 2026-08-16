// Package ms600 projects dynamically discovered Matter nodes into restate-go routes.
// Despite the package name, the implementation is intentionally device-model agnostic.
package ms600

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	descriptorClusterID        uint32 = 0x001d
	basicInformationClusterID  uint32 = 0x0028
	identifyClusterID          uint32 = 0x0003
	groupsClusterID            uint32 = 0x0004
	scenesClusterID            uint32 = 0x0005
	fixedLabelClusterID        uint32 = 0x0040
	userLabelClusterID         uint32 = 0x0041
	partsListAttributeID       uint32 = 0x0003
	serverListAttributeID      uint32 = 0x0001
	attributeListAttributeID   uint32 = 0xfffb
	featureMapAttributeID      uint32 = 0xfffc
	acceptedCommandsAttribute  uint32 = 0xfff9
	generatedCommandsAttribute uint32 = 0xfff8
)

type MatterDevice struct {
	Name      string
	NodeID    uint64
	Endpoints map[uint16]*MatterEndpoint
}

type MatterEndpoint struct {
	ID       uint16
	Clusters map[uint32]*MatterCluster
}

type MatterCluster struct {
	ID                uint32
	Name              string
	FeatureMap        uint32
	GeneratedCommands []uint32
	Attributes        map[uint32]*MatterAttribute
	Commands          map[uint32]*MatterCommand
}

type MatterAttribute struct {
	ID   uint32
	Name string
}

type MatterCommand struct {
	ID   uint32
	Name string
}

type routeTarget struct {
	Endpoint  uint16
	Cluster   uint32
	Attribute *uint32
	Command   *uint32
	Path      string
}

var nonAlphaNumeric = regexp.MustCompile(`[^a-z0-9]+`)

// kebab converts Matter's UpperCamel/acronym-heavy schema names into stable URL names.
func kebab(value string) string {
	var out []rune
	runes := []rune(strings.TrimSpace(value))
	for i, r := range runes {
		if unicode.IsUpper(r) && i > 0 && (unicode.IsLower(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			out = append(out, '-')
		}
		out = append(out, unicode.ToLower(r))
	}
	return strings.Trim(nonAlphaNumeric.ReplaceAllString(string(out), "-"), "-")
}

func numericName(kind string, id uint32) string { return fmt.Sprintf("%s-0x%08x", kind, id) }

// projectRoutes only exposes endpoint IDs when flattening would collide. The endpoint
// qualifier is stable and deliberately located after the device name.
func projectRoutes(device *MatterDevice) []routeTarget {
	var targets []routeTarget
	endpointIDs := make([]int, 0, len(device.Endpoints))
	for id := range device.Endpoints {
		endpointIDs = append(endpointIDs, int(id))
	}
	sort.Ints(endpointIDs)
	for _, endpointID := range endpointIDs {
		ep := device.Endpoints[uint16(endpointID)]
		clusterIDs := make([]int, 0, len(ep.Clusters))
		for id := range ep.Clusters {
			clusterIDs = append(clusterIDs, int(id))
		}
		sort.Ints(clusterIDs)
		for _, clusterID := range clusterIDs {
			cluster := ep.Clusters[uint32(clusterID)]
			if !publicMatterCluster(cluster.ID) {
				continue
			}
			clusterName := kebab(cluster.Name)
			attributeIDs := sortedMapKeys(cluster.Attributes)
			for _, id := range attributeIDs {
				attr := cluster.Attributes[uint32(id)]
				if !publicMatterAttribute(attr.ID) {
					continue
				}
				aid := attr.ID
				targets = append(targets, routeTarget{Endpoint: ep.ID, Cluster: cluster.ID, Attribute: &aid, Path: clusterName + "/" + kebab(attr.Name)})
			}
			commandIDs := sortedMapKeys(cluster.Commands)
			for _, id := range commandIDs {
				command := cluster.Commands[uint32(id)]
				cid := command.ID
				targets = append(targets, routeTarget{Endpoint: ep.ID, Cluster: cluster.ID, Command: &cid, Path: clusterName + "/commands/" + kebab(command.Name)})
			}
		}
	}

	counts := make(map[string]int)
	for _, target := range targets {
		counts[target.Path]++
	}
	for i := range targets {
		if counts[targets[i].Path] > 1 {
			targets[i].Path = fmt.Sprintf("%d/%s", targets[i].Endpoint, targets[i].Path)
		}
	}
	return targets
}

// Descriptor and the global metadata attributes describe the Matter data model
// itself. Discovery consumes them, but projecting them produces repeated routes
// that are not device capabilities and forces otherwise unique routes to carry
// endpoint qualifiers.
func publicMatterCluster(id uint32) bool {
	switch id {
	case descriptorClusterID,
		identifyClusterID,
		groupsClusterID,
		scenesClusterID,
		fixedLabelClusterID,
		userLabelClusterID:
		return false
	default:
		return true
	}
}

func publicMatterAttribute(id uint32) bool {
	switch id {
	case generatedCommandsAttribute, acceptedCommandsAttribute, 0xfffa, attributeListAttributeID, featureMapAttributeID, 0xfffd:
		return false
	default:
		return true
	}
}

func sortedMapKeys[T any](values map[uint32]T) []int {
	keys := make([]int, 0, len(values))
	for key := range values {
		keys = append(keys, int(key))
	}
	sort.Ints(keys)
	return keys
}

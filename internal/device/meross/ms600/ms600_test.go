package ms600

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/kennedn/restate-go/internal/common/config"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type fakeClient struct {
	ids          map[[3]uint32][]uint32
	values       map[[3]uint32]any
	invoked      []routeTarget
	commissioned int
}

func key(endpoint uint16, cluster, attribute uint32) [3]uint32 {
	return [3]uint32{uint32(endpoint), cluster, attribute}
}
func (f *fakeClient) Commission() error   { f.commissioned++; return nil }
func (f *fakeClient) StartSession() error { return nil }
func (f *fakeClient) CloseSession()       {}
func (f *fakeClient) Read(endpoint uint16, cluster, attribute uint32) (any, error) {
	return f.values[key(endpoint, cluster, attribute)], nil
}
func (f *fakeClient) ReadIDs(endpoint uint16, cluster, attribute uint32) ([]uint32, error) {
	return f.ids[key(endpoint, cluster, attribute)], nil
}
func (f *fakeClient) Invoke(endpoint uint16, cluster, command uint32, _ []byte) error {
	commandID := command
	f.invoked = append(f.invoked, routeTarget{Endpoint: endpoint, Cluster: cluster, Command: &commandID})
	return nil
}

func sensorClient() *fakeClient {
	return &fakeClient{
		ids: map[[3]uint32][]uint32{
			key(0, descriptorClusterID, serverListAttributeID):           {basicInformationClusterID},
			key(0, descriptorClusterID, partsListAttributeID):            {1, 2},
			key(0, basicInformationClusterID, attributeListAttributeID):  {1, 3, 0xfffb, 0xfffc, 0xfffd},
			key(0, basicInformationClusterID, acceptedCommandsAttribute): {},
			key(1, descriptorClusterID, serverListAttributeID):           {0x0406},
			key(2, descriptorClusterID, serverListAttributeID):           {0x0400},
			key(1, 0x0406, attributeListAttributeID):                     {0},
			key(2, 0x0400, attributeListAttributeID):                     {0},
			key(1, 0x0406, acceptedCommandsAttribute):                    {},
			key(2, 0x0400, acceptedCommandsAttribute):                    {},
		},
		values: map[[3]uint32]any{
			key(1, 0x0406, 0): uint64(1),
			key(2, 0x0400, 0): uint64(100),
		},
	}
}

func TestDiscoveryProjectsMS600CapabilitiesWithoutHardcoding(t *testing.T) {
	client := sensorClient()
	device, err := discover("office", 42, client)
	require.NoError(t, err)
	targets := projectRoutes(device)
	require.Len(t, targets, 4)
	require.Equal(t, "basic-information/vendor-name", targets[0].Path)
	require.Equal(t, uint16(0), targets[0].Endpoint)
	require.Equal(t, "basic-information/product-name", targets[1].Path)
	require.Equal(t, "occupancy-sensing/occupancy", targets[2].Path)
	require.Equal(t, "illuminance-measurement/measured-value", targets[3].Path)
}

func TestProjectionQualifiesOnlyCollidingEndpoints(t *testing.T) {
	device := &MatterDevice{Name: "switches", Endpoints: map[uint16]*MatterEndpoint{}}
	for _, endpoint := range []uint16{3, 1} {
		device.Endpoints[endpoint] = &MatterEndpoint{ID: endpoint, Clusters: map[uint32]*MatterCluster{
			6: {ID: 6, Name: "OnOff", Attributes: map[uint32]*MatterAttribute{0: {ID: 0, Name: "OnOff"}}, Commands: map[uint32]*MatterCommand{}},
		}}
	}
	targets := projectRoutes(device)
	require.Equal(t, "1/on-off/on-off", targets[0].Path)
	require.Equal(t, "3/on-off/on-off", targets[1].Path)
}

func TestProjectionHidesMatterPlumbing(t *testing.T) {
	device := &MatterDevice{Name: "sensor", Endpoints: map[uint16]*MatterEndpoint{
		1: {ID: 1, Clusters: map[uint32]*MatterCluster{
			descriptorClusterID: {
				ID: descriptorClusterID, Name: "Descriptor",
				Attributes: map[uint32]*MatterAttribute{partsListAttributeID: {ID: partsListAttributeID, Name: "PartsList"}},
				Commands:   map[uint32]*MatterCommand{},
			},
			0x0406: {
				ID: 0x0406, Name: "OccupancySensing",
				Attributes: map[uint32]*MatterAttribute{
					0:                        {ID: 0, Name: "Occupancy"},
					attributeListAttributeID: {ID: attributeListAttributeID, Name: "AttributeList"},
					featureMapAttributeID:    {ID: featureMapAttributeID, Name: "FeatureMap"},
					0xfffd:                   {ID: 0xfffd, Name: "ClusterRevision"},
				},
				Commands: map[uint32]*MatterCommand{},
			},
		}},
	}}
	targets := projectRoutes(device)
	require.Len(t, targets, 1)
	require.Equal(t, "occupancy-sensing/occupancy", targets[0].Path)
}

func TestProjectionHidesReusableUtilityClusters(t *testing.T) {
	for _, clusterID := range []uint32{
		descriptorClusterID,
		identifyClusterID,
		groupsClusterID,
		scenesClusterID,
		fixedLabelClusterID,
		userLabelClusterID,
	} {
		require.Falsef(t, publicMatterCluster(clusterID), "cluster 0x%04x should be hidden", clusterID)
	}
	require.True(t, publicMatterCluster(basicInformationClusterID))
	require.True(t, publicMatterCluster(0x0400))
	require.True(t, publicMatterCluster(0x0406))
}

func TestAttributeAndCommandHandlers(t *testing.T) {
	client := sensorClient()
	attribute := uint32(0)
	handler := targetHandler(client, routeTarget{Endpoint: 1, Cluster: 0x0406, Attribute: &attribute})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Data uint64 `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, uint64(1), response.Data)

	command := uint32(2)
	handler = targetHandler(client, routeTarget{Endpoint: 4, Cluster: 6, Command: &command})
	recorder = httptest.NewRecorder()
	handler(recorder, httptest.NewRequest(http.MethodPost, "/", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, client.invoked, 1)
	require.Equal(t, uint16(4), client.invoked[0].Endpoint)
}

func TestStateIsNamespacedAndPrivate(t *testing.T) {
	stateRoot := t.TempDir()
	directory := stateDirectory(stateRoot, "../Office Sensor")
	require.Equal(t, stateRoot, filepath.Dir(directory))
	state, exists, err := loadOrCreateState(directory)
	require.NoError(t, err)
	require.False(t, exists)
	require.NotZero(t, state.FabricID)
	info, err := os.Stat(filepath.Join(directory, "state.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	loaded, exists, err := loadOrCreateState(directory)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, state, loaded)
}

func TestKebabAcronymsAndFallbacks(t *testing.T) {
	require.Equal(t, "occupancy-sensing", kebab("OccupancySensing"))
	require.Equal(t, "pir-occupied-to-unoccupied-delay", kebab("PIROccupiedToUnoccupiedDelay"))
	require.Equal(t, "cluster-0x12345678", numericName("cluster", 0x12345678))
}

func TestRoutesCommissionOnceThenRestore(t *testing.T) {
	stateRoot := t.TempDir()
	client := sensorClient()
	device := &Device{clientFactory: func(_ matterConfig, _ string, _ persistedState, pin uint32) (matterClient, error) {
		require.NotZero(t, pin)
		return client, nil
	}}
	rawConfig, err := os.ReadFile("testdata/matterConfig/normal_config.yaml")
	require.NoError(t, err)
	cfg := &config.Config{}
	require.NoError(t, yaml.Unmarshal(rawConfig, cfg))
	internalConfig := []byte("stateRoot: " + stateRoot + "\nmatterPort: 5540\n")
	_, deviceRoutes, err := routes(cfg, &internalConfig, device.clientFactory)
	require.NoError(t, err)
	require.Len(t, deviceRoutes, 5)
	require.Equal(t, 1, client.commissioned)
	_, deviceRoutes, err = routes(cfg, &internalConfig, device.clientFactory)
	require.NoError(t, err)
	require.Len(t, deviceRoutes, 5)
	require.Equal(t, 1, client.commissioned)
}

func TestInternalConfigFixtures(t *testing.T) {
	cfg := &config.Config{}
	for _, fixture := range []string{"testdata/baseConfig/empty.yaml", "testdata/baseConfig/non_yaml_config.yaml"} {
		internalConfig, err := os.ReadFile(fixture)
		require.NoError(t, err)
		_, _, err = routes(cfg, &internalConfig, nil)
		require.Error(t, err)
	}
}

func TestMissingMatterConfigFixture(t *testing.T) {
	rawConfig, err := os.ReadFile("testdata/matterConfig/missing_config.yaml")
	require.NoError(t, err)
	cfg := &config.Config{}
	require.NoError(t, yaml.Unmarshal(rawConfig, cfg))
	internalConfig := []byte("stateRoot: " + t.TempDir() + "\nmatterPort: 5540\n")
	_, _, err = routes(cfg, &internalConfig, func(matterConfig, string, persistedState, uint32) (matterClient, error) {
		t.Fatal("client factory must not run for invalid configuration")
		return nil, nil
	})
	require.ErrorContains(t, err, "matterQR")
}

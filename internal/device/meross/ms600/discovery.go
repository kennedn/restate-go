package ms600

import "fmt"

var globalAttributeNames = map[uint32]string{
	0xfff8: "GeneratedCommandList",
	0xfff9: "AcceptedCommandList",
	0xfffa: "EventList",
	0xfffb: "AttributeList",
	0xfffc: "FeatureMap",
	0xfffd: "ClusterRevision",
}

func discover(name string, nodeID uint64, client matterClient) (*MatterDevice, error) {
	device := &MatterDevice{Name: name, NodeID: nodeID, Endpoints: make(map[uint16]*MatterEndpoint)}

	// Endpoint 0 primarily contains Matter administration plumbing, but the
	// mandatory BasicInformation cluster is useful device metadata. Discover it
	// as a curated exception without projecting the other root-node clusters.
	rootClusters, err := client.ReadIDs(0, descriptorClusterID, serverListAttributeID)
	if err != nil {
		return nil, fmt.Errorf("read endpoint 0 Descriptor.ServerList: %w", err)
	}
	for _, clusterID := range rootClusters {
		if clusterID != basicInformationClusterID {
			continue
		}
		cluster, err := discoverCluster(client, 0, clusterID)
		if err != nil {
			return nil, fmt.Errorf("discover endpoint 0 BasicInformation: %w", err)
		}
		device.Endpoints[0] = &MatterEndpoint{ID: 0, Clusters: map[uint32]*MatterCluster{clusterID: cluster}}
		break
	}

	endpointIDs, err := client.ReadIDs(0, descriptorClusterID, partsListAttributeID)
	if err != nil {
		return nil, fmt.Errorf("read Descriptor.PartsList: %w", err)
	}
	for _, rawEndpoint := range endpointIDs {
		if rawEndpoint > 0xffff {
			return nil, fmt.Errorf("invalid Matter endpoint %d", rawEndpoint)
		}
		endpointID := uint16(rawEndpoint)
		clusterIDs, err := client.ReadIDs(endpointID, descriptorClusterID, serverListAttributeID)
		if err != nil {
			return nil, fmt.Errorf("read endpoint %d Descriptor.ServerList: %w", endpointID, err)
		}
		endpoint := &MatterEndpoint{ID: endpointID, Clusters: make(map[uint32]*MatterCluster)}
		for _, clusterID := range clusterIDs {
			cluster, err := discoverCluster(client, endpointID, clusterID)
			if err != nil {
				return nil, fmt.Errorf("discover endpoint %d cluster 0x%08x: %w", endpointID, clusterID, err)
			}
			endpoint.Clusters[clusterID] = cluster
		}
		device.Endpoints[endpointID] = endpoint
	}
	return device, nil
}

func discoverCluster(client matterClient, endpoint uint16, clusterID uint32) (*MatterCluster, error) {
	schema, known := matterSchema[clusterID]
	name := schema.Name
	if !known || name == "" {
		name = numericName("cluster", clusterID)
	}
	cluster := &MatterCluster{ID: clusterID, Name: name, Attributes: make(map[uint32]*MatterAttribute), Commands: make(map[uint32]*MatterCommand)}
	attributes, err := client.ReadIDs(endpoint, clusterID, attributeListAttributeID)
	if err != nil {
		return nil, fmt.Errorf("read AttributeList: %w", err)
	}
	for _, id := range attributes {
		attributeName := schema.Attributes[id]
		if attributeName == "" {
			attributeName = globalAttributeNames[id]
		}
		if attributeName == "" {
			attributeName = numericName("attribute", id)
		}
		cluster.Attributes[id] = &MatterAttribute{ID: id, Name: attributeName}
	}
	commands, err := client.ReadIDs(endpoint, clusterID, acceptedCommandsAttribute)
	if err != nil {
		return nil, fmt.Errorf("read AcceptedCommandList: %w", err)
	}
	for _, id := range commands {
		commandName := schema.Commands[id]
		if commandName == "" {
			commandName = numericName("command", id)
		}
		cluster.Commands[id] = &MatterCommand{ID: id, Name: commandName}
	}
	generated, err := client.ReadIDs(endpoint, clusterID, generatedCommandsAttribute)
	if err == nil {
		cluster.GeneratedCommands = generated
	}
	feature, err := client.Read(endpoint, clusterID, featureMapAttributeID)
	if err == nil {
		switch value := feature.(type) {
		case uint64:
			cluster.FeatureMap = uint32(value)
		}
	}
	return cluster, nil
}

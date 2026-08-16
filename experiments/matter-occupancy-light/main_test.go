package main

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tom-code/gomat/mattertlv"
)

func TestOccupancyFromReportFindsMatchingAttribute(t *testing.T) {
	var encoded mattertlv.TLVBuffer
	encoded.WriteAnonStruct()
	encoded.WriteUInt32(0, 123) // SubscriptionID
	encoded.WriteArray(1)       // AttributeReports
	encoded.WriteAnonStruct()   // AttributeReportIB
	encoded.WriteStruct(1)      // AttributeData
	encoded.WriteUInt32(0, 1)   // DataVersion
	encoded.WriteList(1)        // AttributePath
	encoded.WriteUInt16(2, 1)
	encoded.WriteUInt32(3, occupancyClusterID)
	encoded.WriteUInt32(4, occupancyAttributeID)
	encoded.WriteStructEnd()
	encoded.WriteUInt8(2, 1) // Data
	encoded.WriteStructEnd()
	encoded.WriteStructEnd()
	encoded.WriteStructEnd()
	encoded.WriteStructEnd()

	value, err := occupancyFromReport(mattertlv.Decode(encoded.Bytes()), 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), value)
}

func TestOccupancyFromReportIgnoresReportWithoutAttributeData(t *testing.T) {
	var encoded mattertlv.TLVBuffer
	encoded.WriteAnonStruct()
	encoded.WriteUInt32(0, 123)
	encoded.WriteStructEnd()

	_, err := occupancyFromReport(mattertlv.Decode(encoded.Bytes()), 1)
	require.True(t, errors.Is(err, errNoOccupancyData))
}

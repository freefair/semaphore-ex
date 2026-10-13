package docker

import (
	"archive/tar"
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadDockerOutputArchiveAcceptsOnlyOneBoundedRegularFile(t *testing.T) {
	validValue := []byte(`{"values":{"image":"v1"}}`)
	maxValue := []byte(strings.Repeat("x", maxContainerOutputsBytes))

	tests := []struct {
		name    string
		archive []byte
		limit   int
		want    []byte
		wantErr bool
	}{
		{name: "no output", archive: nil, limit: maxContainerOutputsBytes},
		{name: "valid", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(validValue))}}, [][]byte{validValue}), limit: maxContainerOutputsBytes, want: validValue},
		{name: "maximum content with tar padding", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(maxValue))}}, [][]byte{maxValue}), limit: maxContainerOutputsBytes, want: maxValue},
		{name: "symlink", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeSymlink, Linkname: "target"}}, nil), limit: maxContainerOutputsBytes, wantErr: true},
		{name: "fifo", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeFifo}}, nil), limit: maxContainerOutputsBytes, wantErr: true},
		{name: "wrong name", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "other.json", Typeflag: tar.TypeReg, Size: int64(len(validValue))}}, [][]byte{validValue}), limit: maxContainerOutputsBytes, wantErr: true},
		{name: "extra member", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeReg, Size: int64(len(validValue))}, {Name: "extra", Typeflag: tar.TypeReg, Size: 1}}, [][]byte{validValue, []byte("x")}), limit: maxContainerOutputsBytes, wantErr: true},
		{name: "oversize", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeReg, Size: int64(maxContainerOutputsBytes + 1)}}, [][]byte{append(maxValue, 'x')}), limit: maxContainerOutputsBytes, wantErr: true},
		{name: "truncated", archive: dockerOutputTestArchive(t, []tar.Header{{Name: "outputs.json", Typeflag: tar.TypeReg, Size: int64(len(validValue) + 1)}}, [][]byte{validValue}), limit: maxContainerOutputsBytes, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := readDockerOutputArchive(tt.archive, "outputs.json", tt.limit)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func dockerOutputTestArchive(t *testing.T, headers []tar.Header, values [][]byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := tar.NewWriter(&buffer)
	for index, header := range headers {
		require.NoError(t, archive.WriteHeader(&header))
		if index < len(values) && len(values[index]) > 0 {
			_, err := archive.Write(values[index])
			require.NoError(t, err)
		}
	}
	if len(headers) > 0 && headers[len(headers)-1].Size > 0 && len(values) > 0 && len(values[len(values)-1]) != int(headers[len(headers)-1].Size) {
		return buffer.Bytes()
	}
	require.NoError(t, archive.Close())
	return buffer.Bytes()
}

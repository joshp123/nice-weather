package climatefacts

import (
	"context"
	_ "embed"
	"encoding/binary"
	"fmt"
	"math"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed pcodec-wasm/decoder.wasm
var pcodecModule []byte

var decoderOnce sync.Once
var decoderError error
var decoderLock sync.Mutex
var decoderModule api.Module

func decodeChunk(encoded []byte) ([]float32, error) {
	decoderOnce.Do(func() {
		ctx := context.Background()
		runtime := wazero.NewRuntime(ctx)
		decoderModule, decoderError = runtime.InstantiateWithConfig(ctx, pcodecModule, wazero.NewModuleConfig().WithStartFunctions())
	})
	if decoderError != nil {
		return nil, decoderError
	}
	decoderLock.Lock()
	defer decoderLock.Unlock()
	ctx := context.Background()
	allocate := decoderModule.ExportedFunction("allocate")
	deallocate := decoderModule.ExportedFunction("deallocate")
	input, err := allocate.Call(ctx, uint64(len(encoded)))
	if err != nil {
		return nil, err
	}
	defer deallocate.Call(ctx, input[0], uint64(len(encoded)))
	const capacity = 8736 * 12 * 12
	output, err := allocate.Call(ctx, capacity*4)
	if err != nil {
		return nil, err
	}
	defer deallocate.Call(ctx, output[0], capacity*4)
	if !decoderModule.Memory().Write(uint32(input[0]), encoded) {
		return nil, fmt.Errorf("write pcodec input outside decoder memory")
	}
	result, err := decoderModule.ExportedFunction("decode").Call(ctx, input[0], uint64(len(encoded)), output[0], capacity)
	if err != nil {
		return nil, fmt.Errorf("decode ERA5 pcodec chunk: %w", err)
	}
	count := int32(result[0])
	if count <= 0 || count > capacity {
		return nil, fmt.Errorf("ERA5 pcodec decoder returned %d values", count)
	}
	data, ok := decoderModule.Memory().Read(uint32(output[0]), uint32(count)*4)
	if !ok {
		return nil, fmt.Errorf("read pcodec output outside decoder memory")
	}
	values := make([]float32, count)
	for index := range values {
		values[index] = math.Float32frombits(binary.LittleEndian.Uint32(data[index*4 : index*4+4]))
	}
	return values, nil
}

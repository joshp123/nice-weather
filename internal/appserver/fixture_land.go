package appserver

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"

	"nice-weather-finder/internal/delivery"
)

const (
	fixtureLandSourceURL    = "https://naciscdn.org/naturalearth/5.1.1/10m/physical/ne_10m_land.zip"
	fixtureLandSourceSHA256 = "e547d749445eaa0964aba76738090ec88f5e63c4585122170f98c67a7ea922dc"
	fixtureLandBitsetBytes  = 5_880
	fixtureLandBitsetSHA256 = "c510f56eca3192073670ef5468ff49ad22d4796390ff8bd3042a395fc8630ddf"
	fixtureLandGzipSHA256   = "080f9301a8d10687e13768ec1e1ad7f55a109292442bb5e8d871c88da688b1b2"
)

// fixtureLandGzipBase64 is the row-major, most-significant-bit-first land
// mask for fixtureGridSpec. It was rasterised once with GDAL's all-touched
// semantics from the pinned Natural Earth 5.1.1 archive identified above.
// Keeping the verified mask in the binary makes fixture generation offline
// and gives sample releases the same land contract as live releases.
const fixtureLandGzipBase64 = "H4sIAAAAAAACA5WYv2/fRBTAbS7NRUqoBwbCD+VA/AEVE5EQMog/gJGxdOpIJQY6VDlDJbIUvhMSC8rQlYGNMUYIMpKNBfR1hfix" +
	"oDgKqA517vHuzmf77p5tYamq/fUnd+/nvfcMdbJ0QbOMtIuI7JC7we8vNP3fCnuXBiKxtYLSIYpcXACAe3NCyvKVRASKTlxKaaaB" +
	"fhWoCCQDe9llgLSEh1D7POcQZZQqCeQSembCmhyGq1xG0IOSQPIRghLnMZGOCVwmI5DvPIZCtpmHFCJGdsUikvhIKWbMb68fuF7Z" +
	"j7htH/mMp9o4JeVmrTG6QJxl6vXAzCNEJXAOIBQas5pA0LboDanwrp1SqNDeMEHaRquotIsFRNIIMV6srYnfqHBNESL2XWn/+0bh" +
	"mju5jYrA0ZUNmofGkRqpooCqbNCYEE1kEKE3rSrSqiQ6JF4E7o8jpj8bPIR7yO06NlztxR0ZCfdnkGsw2HYCybqwp5DUqmV1rUmE" +
	"V6McqkjE5v//QeQkwtx5ICc1WkSKAZmwCy+cLG26iCi9z2MCeXY4WxA5/5xA2JDPX8q/kyMC2RiQEuNGxIj2ktuoGh94PbHFqmGV" +
	"hJMasdJ4+tLkVUYhr5oTX0KJP556id0jF2BDV+9VlBSSXphsYq12T5EIESNvnZmsTYunUBdePn8SIBsmf2xm7+WgsiTn8SrMITla" +
	"dZXIQyJ2pa2SHFZ4SpLxsplbJGtxix06Xrrsz+AV/JGOl2N7tGaY8WoCyd0qAu0/hRiNMmUOExL50B6KmT0cKSQ7dsgVFPRG8tIe" +
	"eSgs65MlQK56JCkOBIWA+sNozf9t8MzNCUSAum20ZvBj7Ve+AWl/NR54BuAJkAh6KDWG+QCiq1/FIhnMII0MDR8gGdQaYTMI19UU" +
	"Kja/ylFfy6Y00hWo5rPII71RNoe0zLQQM4gwknqBH4lrku1bmEFshVgvI/k8grZ/sIAwFXR0McIfJLRCA/Ky3znSB/zNbBGht/Fr" +
	"wCLCV4sIwBKSLiOT0sJEszwh7jLClpHJnRaQEx+5QSC5j2Tx+0oEpTwgsM0tAyRUqcXJKEBCFzQMipxElNPMZXiMtDn47dLMKljy" +
	"T4EUtx2GuQPjfgLhveH3ja1CpJWue1Ruc/d+p0OaDIrxKDdCctgwG5TcNdXKxWIY3JgqdTZCRI+4bu0geRfq3A171nE+Umwl70BX" +
	"SWpnLN9sWM/+guaaHBAUxpelTNhvoKzgtdMjQiqs6Wkny60iRnAjjCLbVzcoahkjuunK8KU0EaXhEMFKneJMo2zyFqZlGhXhTsKn" +
	"dZQUfX6fPw4QnQZVuoZKP11EddohtS2CT26QiPYKr9GMLRbB94FC2mRXzwMp7El1L8ppayz2sxkZDl6UcGdNIjUvDSLQGB+t4gpr" +
	"ZrgOKQT8chQiJhjqW1pp/FeghQWJNJVp45NVweB3TiPvmfvDPXxsJC1ud69QtFaQdik7yVvdcc4hOhhwXzaJpLgKBpaENYWo7uuT" +
	"ibV/3qYQnTibj66KXCOfnEfIlkQkhWOAL7hGfrqk2tBTGzS72B0mrx1+r0egMx8pu1O1m5Xu3FWZ9UKP7LnBX58j2Own+/d47SPX" +
	"0y5R0k39+GbytZuLhynZ5fXH5nEfh7vkeu4jnx6OkfwKu942WMX/pJVj2vFA3Nz/FLX9Z4zI+GsVL2kk/rgRFftmEmHgi+tnelD1" +
	"JhFJflzrnfaS13c0BKIHxnHRq00SDEdtN5mOS6dKTPyOEb2RV11LFD5AkgjpigOLTgYPKULEawYqjWDWnvJJpDGRUYqukJIthenB" +
	"+2JMIo0g54Cg56CmCZjt63bnkf8AsYTGD/gWAAA="

var fixtureGridSpec = delivery.GridSpec{
	Bounds: delivery.GridBounds{West: -25, East: 45, South: 30, North: 72},
	Rows:   168,
	Cols:   280,
}

func fixtureLandMask() ([]byte, error) {
	compressed, err := base64.StdEncoding.Strict().DecodeString(fixtureLandGzipBase64)
	if err != nil {
		return nil, fmt.Errorf("decode embedded fixture land mask: %w", err)
	}
	if got := sha256Hex(compressed); got != fixtureLandGzipSHA256 {
		return nil, fmt.Errorf("embedded fixture land gzip SHA256 is %s; want %s", got, fixtureLandGzipSHA256)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, fmt.Errorf("open embedded fixture land mask: %w", err)
	}
	reader.Multistream(false)
	mask, readErr := io.ReadAll(io.LimitReader(reader, fixtureLandBitsetBytes+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompress embedded fixture land mask: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close embedded fixture land mask: %w", closeErr)
	}
	if len(mask) != fixtureLandBitsetBytes {
		return nil, fmt.Errorf("embedded fixture land mask has %d bytes; want %d", len(mask), fixtureLandBitsetBytes)
	}
	if got := sha256Hex(mask); got != fixtureLandBitsetSHA256 {
		return nil, fmt.Errorf("embedded fixture land mask SHA256 is %s; want %s", got, fixtureLandBitsetSHA256)
	}
	if cells := uint64(fixtureGridSpec.Rows) * uint64(fixtureGridSpec.Cols); cells != uint64(len(mask))*8 {
		return nil, fmt.Errorf("embedded fixture land mask has %d bits for %d grid cells", len(mask)*8, cells)
	}
	return mask, nil
}

func fixtureLandCell(mask []byte, index int) bool {
	return mask[index/8]&(byte(1)<<uint(7-index%8)) != 0
}

func sha256Hex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

package emutest

// Seeded storage: an instance started with Options.Storage boots with the
// state in that directory, the kit's emulated NVS, one file per namespace
// (the kit's src/emu/kv_file.c). SeedStorage writes such a file, so a test
// can boot a pager with state no pairing would leave, such as a damaged key.

import (
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// A KVType is how the kit stores a value: reading it as the other type fails.
type KVType byte

const (
	KVStr  KVType = 1 // LCD349_KV_STR
	KVBlob KVType = 2 // LCD349_KV_BLOB
)

// A KV is one stored key.
type KV struct {
	Key  string
	Type KVType
	Data []byte
}

// SeedStorage writes namespace ns, holding kvs, into the storage directory
// dir in the kit's format ("L349KV1\n", the name, then each key's name, type,
// little-endian length and bytes), and returns the file's path.
func SeedStorage(t testing.TB, dir, ns string, kvs ...KV) string {
	t.Helper()
	buf := []byte("L349KV1\n")
	buf = append(buf, byte(len(ns)))
	buf = append(buf, ns...)
	for _, kv := range kvs {
		buf = append(buf, byte(len(kv.Key)))
		buf = append(buf, kv.Key...)
		buf = append(buf, byte(kv.Type))
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(kv.Data)))
		buf = append(buf, kv.Data...)
	}
	path := filepath.Join(dir, "ns-"+hex.EncodeToString([]byte(ns))+".kv")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

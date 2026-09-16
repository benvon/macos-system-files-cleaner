//go:build darwin

package scan

import (
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseBulkRecordRejectsTruncation(t *testing.T) {
	t.Parallel()
	if _, err := parseBulkRecord(make([]byte, 23)); err == nil {
		t.Fatal("parseBulkRecord() accepted a truncated record")
	}
}

func TestParseBulkRecordRejectsNonTerminatedName(t *testing.T) {
	t.Parallel()
	record := make([]byte, 33)
	binary.LittleEndian.PutUint32(record[0:], uint32(len(record)))
	binary.LittleEndian.PutUint32(record[4:], unix.ATTR_CMN_NAME)
	binary.LittleEndian.PutUint32(record[24:], 8)
	binary.LittleEndian.PutUint32(record[28:], 1)
	record[32] = 'x'
	if _, err := parseBulkRecord(record); err == nil {
		t.Fatal("parseBulkRecord() accepted a non-null-terminated name")
	}
}

func TestParseBulkRecord(t *testing.T) {
	t.Parallel()
	const name = "cache.data"
	const fixedLength = 4 + 20 + 8 + 4 + 4 + 8 + 4 + 8 + 8
	record := make([]byte, fixedLength+len(name)+1)
	binary.LittleEndian.PutUint32(record[0:], uint32(len(record)))
	binary.LittleEndian.PutUint32(record[4:], unix.ATTR_CMN_NAME|unix.ATTR_CMN_DEVID|unix.ATTR_CMN_OBJTYPE|unix.ATTR_CMN_FILEID)
	binary.LittleEndian.PutUint32(record[16:], unix.ATTR_FILE_LINKCOUNT|unix.ATTR_FILE_TOTALSIZE|unix.ATTR_FILE_ALLOCSIZE)

	position := 24
	binary.LittleEndian.PutUint32(record[position:], uint32(fixedLength-position))
	binary.LittleEndian.PutUint32(record[position+4:], uint32(len(name)+1))
	position += 8
	binary.LittleEndian.PutUint32(record[position:], 42)
	position += 4
	binary.LittleEndian.PutUint32(record[position:], vnodeRegular)
	position += 4
	binary.LittleEndian.PutUint64(record[position:], 99)
	position += 8
	binary.LittleEndian.PutUint32(record[position:], 2)
	position += 4
	binary.LittleEndian.PutUint64(record[position:], 1234)
	position += 8
	binary.LittleEndian.PutUint64(record[position:], 4096)
	copy(record[fixedLength:], name)

	got, err := parseBulkRecord(record)
	if err != nil {
		t.Fatalf("parseBulkRecord() error = %v", err)
	}
	if got.name != name || got.kind != kindRegular || got.device != 42 || got.inode != 99 || got.links != 2 || got.apparent != 1234 || got.allocated != 4096 {
		t.Fatalf("parseBulkRecord() = %#v", got)
	}
}

func TestParseBulkRecordExtendedAPFSAttributes(t *testing.T) {
	t.Parallel()
	const fixedLength = 4 + 20 + 8 + 8 + 4
	record := make([]byte, fixedLength)
	binary.LittleEndian.PutUint32(record[0:], uint32(len(record)))
	binary.LittleEndian.PutUint32(record[20:], attrCommonPrivateSize|attrCommonExtendedFlags|attrCommonCloneRefCount)

	position := 24
	binary.LittleEndian.PutUint64(record[position:], 8192)
	position += 8
	binary.LittleEndian.PutUint64(record[position:], extFlagMayShareBlocks|extFlagSparse)
	position += 8
	binary.LittleEndian.PutUint32(record[position:], 3)

	got, err := parseBulkRecord(record)
	if err != nil {
		t.Fatalf("parseBulkRecord() error = %v", err)
	}
	if !got.privateKnown || got.private != 8192 || got.cloneRefs != 3 {
		t.Fatalf("parseBulkRecord() = %#v", got)
	}
	if got.extFlags != extFlagMayShareBlocks|extFlagSparse {
		t.Fatalf("extFlags = %#x", got.extFlags)
	}
}

func FuzzParseBulkRecord(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 24))
	f.Add([]byte{24, 0, 0, 0})
	f.Fuzz(func(t *testing.T, record []byte) {
		_, _ = parseBulkRecord(record)
	})
}

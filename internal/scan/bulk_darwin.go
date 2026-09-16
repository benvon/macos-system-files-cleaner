//go:build darwin

package scan

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

const (
	vnodeRegular   = 1
	vnodeDirectory = 2
	vnodeSymlink   = 5
	bulkBufferSize = 128 << 10

	// These public Darwin constants are present in <sys/attr.h> but are not
	// currently exported by golang.org/x/sys/unix.
	attrDirMountStatus = 0x00000004
	attrDirAllocSize   = 0x00000008
	attrDirDataLength  = 0x00000020

	fsoptAttrCommonExtended = 0x00000020
	attrCommonPrivateSize   = 0x00000008
	attrCommonExtendedFlags = 0x00000200
	attrCommonCloneRefCount = 0x00001000

	extFlagMayShareBlocks  = 0x00000001
	extFlagSyncRoot        = 0x00000004
	extFlagPurgeable       = 0x00000008
	extFlagSparse          = 0x00000010
	extFlagSynthetic       = 0x00000020
	extFlagSharesAllBlocks = 0x00000040

	dirMountStatusMountPoint = 0x00000001
	dirMountStatusTrigger    = 0x00000002
)

type entryKind uint8

const (
	kindOther entryKind = iota
	kindRegular
	kindDirectory
	kindSymlink
)

type entry struct {
	name      string
	kind      entryKind
	device    uint64
	inode     uint64
	links     uint32
	mount     uint32
	apparent  uint64
	allocated uint64
	private   uint64
	extFlags  uint64
	cloneRefs uint32

	privateKnown bool
}

func readDirectory(path string) ([]entry, bool, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW_ANY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, err
	}
	defer unix.Close(fd)

	attributes := unix.Attrlist{
		Bitmapcount: unix.ATTR_BIT_MAP_COUNT,
		Commonattr: unix.ATTR_CMN_RETURNED_ATTRS | unix.ATTR_CMN_NAME |
			unix.ATTR_CMN_DEVID | unix.ATTR_CMN_OBJTYPE | unix.ATTR_CMN_FILEID,
		Dirattr: attrDirMountStatus | attrDirAllocSize | attrDirDataLength,
		Fileattr: unix.ATTR_FILE_LINKCOUNT | unix.ATTR_FILE_TOTALSIZE |
			unix.ATTR_FILE_ALLOCSIZE,
		Forkattr: attrCommonPrivateSize | attrCommonExtendedFlags |
			attrCommonCloneRefCount,
	}

	buffer := make([]byte, bulkBufferSize)
	var entries []entry
	for {
		count, err := getattrlistbulk(fd, &attributes, buffer)
		if err != nil {
			if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
				if len(entries) != 0 {
					return nil, false, fmt.Errorf("bulk enumeration failed after partial results: %w", err)
				}
				fallback, fallbackErr := fallbackDirectory(fd)
				return fallback, true, fallbackErr
			}
			return nil, false, err
		}
		if count == 0 {
			return entries, false, nil
		}
		offset := 0
		for i := 0; i < count; i++ {
			if offset+4 > len(buffer) {
				return nil, false, errors.New("truncated getattrlistbulk record length")
			}
			recordLength := int(binary.LittleEndian.Uint32(buffer[offset:]))
			if recordLength < 24 || offset+recordLength > len(buffer) {
				return nil, false, fmt.Errorf("invalid getattrlistbulk record length %d", recordLength)
			}
			item, err := parseBulkRecord(buffer[offset : offset+recordLength])
			if err != nil {
				return nil, false, err
			}
			if item.name != "" && item.name != "." && item.name != ".." {
				entries = append(entries, item)
			}
			offset += recordLength
		}
	}
}

func parseBulkRecord(record []byte) (entry, error) {
	var item entry
	if len(record) < 24 {
		return item, errors.New("getattrlistbulk record is too short")
	}
	position := 4
	returnedCommon := binary.LittleEndian.Uint32(record[position:])
	returnedDirectory := binary.LittleEndian.Uint32(record[position+8:])
	returnedFile := binary.LittleEndian.Uint32(record[position+12:])
	returnedExtended := binary.LittleEndian.Uint32(record[position+16:])
	position += 20

	need := func(bytes int) error {
		if bytes < 0 || position+bytes > len(record) {
			return errors.New("truncated getattrlistbulk attribute")
		}
		return nil
	}

	if returnedCommon&unix.ATTR_CMN_NAME != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		nameOffset := int(int32(binary.LittleEndian.Uint32(record[position:])))
		nameLength := int(binary.LittleEndian.Uint32(record[position+4:]))
		nameStart := position + nameOffset
		if nameStart < 0 || nameLength < 1 || nameStart+nameLength > len(record) {
			return item, errors.New("invalid name reference in getattrlistbulk record")
		}
		nameBytes := record[nameStart : nameStart+nameLength]
		if nameBytes[len(nameBytes)-1] != 0 || bytes.IndexByte(nameBytes[:len(nameBytes)-1], 0) >= 0 {
			return item, errors.New("invalid null-terminated name in getattrlistbulk record")
		}
		item.name = string(nameBytes[:len(nameBytes)-1])
		position += 8
	}
	if returnedCommon&unix.ATTR_CMN_DEVID != 0 {
		if err := need(4); err != nil {
			return item, err
		}
		item.device = uint64(binary.LittleEndian.Uint32(record[position:]))
		position += 4
	}
	if returnedCommon&unix.ATTR_CMN_OBJTYPE != 0 {
		if err := need(4); err != nil {
			return item, err
		}
		switch binary.LittleEndian.Uint32(record[position:]) {
		case vnodeRegular:
			item.kind = kindRegular
		case vnodeDirectory:
			item.kind = kindDirectory
		case vnodeSymlink:
			item.kind = kindSymlink
		}
		position += 4
	}
	if returnedCommon&unix.ATTR_CMN_FILEID != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.inode = binary.LittleEndian.Uint64(record[position:])
		position += 8
	}
	if returnedDirectory&attrDirMountStatus != 0 {
		if err := need(4); err != nil {
			return item, err
		}
		item.mount = binary.LittleEndian.Uint32(record[position:])
		position += 4
	}
	if returnedDirectory&attrDirAllocSize != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.allocated = binary.LittleEndian.Uint64(record[position:])
		position += 8
	}
	if returnedDirectory&attrDirDataLength != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.apparent = binary.LittleEndian.Uint64(record[position:])
		position += 8
	}
	if returnedFile&unix.ATTR_FILE_LINKCOUNT != 0 {
		if err := need(4); err != nil {
			return item, err
		}
		item.links = binary.LittleEndian.Uint32(record[position:])
		position += 4
	}
	if returnedFile&unix.ATTR_FILE_TOTALSIZE != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.apparent = binary.LittleEndian.Uint64(record[position:])
		position += 8
	}
	if returnedFile&unix.ATTR_FILE_ALLOCSIZE != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.allocated = binary.LittleEndian.Uint64(record[position:])
		position += 8
	}
	if returnedExtended&attrCommonPrivateSize != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.private = binary.LittleEndian.Uint64(record[position:])
		item.privateKnown = true
		position += 8
	}
	if returnedExtended&attrCommonExtendedFlags != 0 {
		if err := need(8); err != nil {
			return item, err
		}
		item.extFlags = binary.LittleEndian.Uint64(record[position:])
		position += 8
	}
	if returnedExtended&attrCommonCloneRefCount != 0 {
		if err := need(4); err != nil {
			return item, err
		}
		item.cloneRefs = binary.LittleEndian.Uint32(record[position:])
	}
	return item, nil
}

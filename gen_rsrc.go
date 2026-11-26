//go:build ignore

// Generates rsrc_windows_amd64.syso containing the Windows application icon
// (RT_GROUP_ICON + RT_ICON) using only the Go standard library.
package main

import (
	"bytes"
	"encoding/binary"
	"os"
)

func main() {
	sizes := []int{16, 32, 48, 256}
	pngs := make([][]byte, len(sizes))
	for i, sz := range sizes {
		pngs[i] = generateNekoLogoPNG(sz)
	}

	// Build RT_GROUP_ICON payload (GRPICONDIR)
	var grp bytes.Buffer
	_ = binary.Write(&grp, binary.LittleEndian, uint16(0))
	_ = binary.Write(&grp, binary.LittleEndian, uint16(1))
	_ = binary.Write(&grp, binary.LittleEndian, uint16(len(sizes)))
	for i, sz := range sizes {
		dim := uint8(sz)
		if sz >= 256 {
			dim = 0
		}
		grp.WriteByte(dim)
		grp.WriteByte(dim)
		grp.WriteByte(0)
		grp.WriteByte(0)
		_ = binary.Write(&grp, binary.LittleEndian, uint16(1))
		_ = binary.Write(&grp, binary.LittleEndian, uint16(32))
		_ = binary.Write(&grp, binary.LittleEndian, uint32(len(pngs[i])))
		_ = binary.Write(&grp, binary.LittleEndian, uint16(i+1)) // RT_ICON id
	}

	rsrc, relocs := buildRsrcSection(pngs, grp.Bytes())
	syso := buildCOFF(rsrc, relocs)
	if err := os.WriteFile("rsrc_windows_amd64.syso", syso, 0o644); err != nil {
		panic(err)
	}
}

type coffReloc struct {
	virtualAddr uint32
	symbolIndex uint32
	typ         uint16
}

func buildRsrcSection(icons [][]byte, group []byte) ([]byte, []coffReloc) {
	// Resource tree:
	// Root (2 entries: RT_ICON=3, RT_GROUP_ICON=14)
	//  ├─ Type 3 -> len(icons) ID entries (1..N) -> Lang 0x0409 -> DataEntry
	//  └─ Type 14 -> 1 ID entry (1) -> Lang 0x0409 -> DataEntry
	var buf bytes.Buffer
	writeDir := func(n int) {
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0)) // Characteristics
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0)) // TimeDateStamp
		_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // MajorVersion
		_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // MinorVersion
		_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // NumberOfNamedEntries
		_ = binary.Write(&buf, binary.LittleEndian, uint16(n)) // NumberOfIdEntries
	}
	writeEntry := func(id uint32, offset uint32, isDir bool) {
		if isDir {
			offset |= 0x80000000
		}
		_ = binary.Write(&buf, binary.LittleEndian, id)
		_ = binary.Write(&buf, binary.LittleEndian, offset)
	}

	// Calculate offsets
	rootOff := uint32(0)
	rootSize := uint32(16 + 2*8)

	type3Off := rootOff + rootSize
	type3Size := uint32(16 + len(icons)*8)

	type14Off := type3Off + type3Size
	type14Size := uint32(16 + 1*8)

	langOff := type14Off + type14Size
	langEntrySize := uint32(16 + 8)
	numLeaves := len(icons) + 1
	dataEntriesOff := langOff + uint32(numLeaves)*langEntrySize
	dataEntrySize := uint32(16)
	rawOff := dataEntriesOff + uint32(numLeaves)*dataEntrySize

	// 1. Root
	writeDir(2)
	writeEntry(3, type3Off, true)
	writeEntry(14, type14Off, true)

	// 2. Type 3 (RT_ICON)
	writeDir(len(icons))
	for i := range icons {
		writeEntry(uint32(i+1), langOff+uint32(i)*langEntrySize, true)
	}

	// 3. Type 14 (RT_GROUP_ICON)
	writeDir(1)
	writeEntry(1, langOff+uint32(len(icons))*langEntrySize, true)

	// 4. Language dirs
	for i := 0; i < numLeaves; i++ {
		writeDir(1)
		writeEntry(0x0409, dataEntriesOff+uint32(i)*dataEntrySize, false)
	}

	// 5. Data entries + relocations (IMAGE_REL_AMD64_ADDR32NB = 3)
	var relocs []coffReloc
	curRaw := rawOff
	writeDataEntry := func(data []byte) {
		rvaPos := uint32(buf.Len())
		relocs = append(relocs, coffReloc{virtualAddr: rvaPos, symbolIndex: 0, typ: 3})
		_ = binary.Write(&buf, binary.LittleEndian, curRaw)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0))
		aligned := uint32((len(data) + 3) &^ 3)
		curRaw += aligned
	}
	for _, ic := range icons {
		writeDataEntry(ic)
	}
	writeDataEntry(group)

	// 6. Raw icon & group payloads (4-byte aligned)
	writeAligned := func(data []byte) {
		buf.Write(data)
		for buf.Len()%4 != 0 {
			buf.WriteByte(0)
		}
	}
	for _, ic := range icons {
		writeAligned(ic)
	}
	writeAligned(group)

	return buf.Bytes(), relocs
}

func buildCOFF(rsrc []byte, relocs []coffReloc) []byte {
	const (
		fileHdrSize    = 20
		secHdrSize     = 40
		symSize        = 18
		relocSize      = 10
		machineAMD64   = 0x8664
		scnCntInitData = 0x00000040
		scnMemRead     = 0x40000000
	)
	rsrcOff := uint32(fileHdrSize + secHdrSize)
	relocOff := rsrcOff + uint32(len(rsrc))
	symTabOff := relocOff + uint32(len(relocs))*relocSize

	var out bytes.Buffer
	// IMAGE_FILE_HEADER
	_ = binary.Write(&out, binary.LittleEndian, uint16(machineAMD64))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // NumberOfSections
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))
	_ = binary.Write(&out, binary.LittleEndian, symTabOff)
	_ = binary.Write(&out, binary.LittleEndian, uint32(1)) // NumberOfSymbols
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // SizeOfOptionalHeader
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))

	// IMAGE_SECTION_HEADER (.rsrc)
	var secName [8]byte
	copy(secName[:], ".rsrc")
	out.Write(secName[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(rsrc)))
	_ = binary.Write(&out, binary.LittleEndian, rsrcOff)
	_ = binary.Write(&out, binary.LittleEndian, relocOff)
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(relocs)))
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))
	_ = binary.Write(&out, binary.LittleEndian, uint32(scnCntInitData|scnMemRead))

	out.Write(rsrc)
	for _, rel := range relocs {
		_ = binary.Write(&out, binary.LittleEndian, rel.virtualAddr)
		_ = binary.Write(&out, binary.LittleEndian, rel.symbolIndex)
		_ = binary.Write(&out, binary.LittleEndian, rel.typ)
	}

	// Symbol 0: ".rsrc" section symbol
	out.Write(secName[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))
	_ = binary.Write(&out, binary.LittleEndian, int16(1)) // SectionNumber 1
	_ = binary.Write(&out, binary.LittleEndian, uint16(0))
	out.WriteByte(3) // IMAGE_SYM_CLASS_STATIC
	out.WriteByte(0)

	// String table (4-byte empty length)
	_ = binary.Write(&out, binary.LittleEndian, uint32(4))
	return out.Bytes()
}

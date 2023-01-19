package ogg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
)

type Page struct {
	//bitfield of COP, EOS, and BOS
	Type byte
	//codec-specific time value
	Granule uint64
	//logical bitstream serial number
	Serial uint32
	//page number for logical bitstream
	Sequence uint32

	Crc uint32

	SegTable []int
	Data     []byte
}

type PageDecoder struct {
	b   io.Reader
	buf []byte
}

type PageEncoder struct {
	b   io.Writer
	buf []byte
}

func NewPageDecoder(backing io.Reader) *PageDecoder {
	return &PageDecoder{
		b:   backing,
		buf: make([]byte, headsz+255), //header length + max length of segment table
	}
}

func NewPageEncoder(backing io.Writer) *PageEncoder {
	return &PageEncoder{
		b:   backing,
		buf: make([]byte, maxPageSize),
	}
}

func (d *PageDecoder) ReadPage() (Page, error) {
	var err error
	var p = Page{}

	var header = d.buf[:headsz]
	_, err = io.ReadFull(d.b, header)
	if err != nil {
		return Page{}, err
	}

	//validate capture pattern
	if !bytes.Equal(header[0:4], []byte{0x4F, 0x67, 0x67, 0x53}) { //OggS
		return Page{}, errors.New("data does not start with capture pattern")
	}

	if header[4:5][0] != 0x00 { //ogg version must currently be zero
		return Page{}, errors.New("ogg version is non-zero")
	}

	p.Type = header[5]

	//p.Granule = int64(^byteOrder.Uint64(header[6:14]) + 1) //read as uint64 and convert to int64 (two's complement)
	p.Granule = byteOrder.Uint64(header[6:14])

	p.Serial = byteOrder.Uint32(header[14:18])

	p.Sequence = byteOrder.Uint32(header[18:22])

	p.Crc = byteOrder.Uint32(header[22:26])

	p.SegTable = make([]int, header[26])

	//clear
	copy(header[22:26], []byte{0, 0, 0, 0}) //crc needs to be calculated with crc field set to 0s
	tCrc := extendCrc32(0, header)          //temporary crc to compare to one from header

	//read segment table
	//todo:maybe rework
	segtbl := d.buf[:len(p.SegTable)]
	_, err = io.ReadFull(d.b, segtbl)
	if err != nil {
		return Page{}, err
	}

	//crc segment table
	tCrc = extendCrc32(tCrc, segtbl)

	//sum segment lengths to get data length
	dsize := 0
	for i := 0; i < len(p.SegTable); i++ {
		p.SegTable[i] = int(segtbl[i])
		dsize += int(segtbl[i])
	}

	p.Data = make([]byte, dsize)
	_, err = io.ReadFull(d.b, p.Data)
	if err != nil {
		return Page{}, err
	}

	tCrc = extendCrc32(tCrc, p.Data)

	//check crc to confirm that data is a valid page header
	if tCrc != p.Crc {
		return Page{}, errors.New(fmt.Sprintf("invalid crc: expected 0x%X, field was 0x%X", tCrc, p.Crc))
	}

	return p, nil
}

// WritePage writes page to writer backing PageEncoder(recalculates CRC instead of using stored value)
func (e *PageEncoder) WritePage(p Page) error {
	copy(e.buf[0:4], []byte{0x4F, 0x67, 0x67, 0x53})
	e.buf[4] = 0

	e.buf[5] = p.Type

	byteOrder.PutUint64(e.buf[6:14], p.Granule)

	byteOrder.PutUint32(e.buf[14:18], p.Serial)

	byteOrder.PutUint32(e.buf[18:22], p.Sequence)

	//need to set crc field to 0 while calculating crc
	byteOrder.PutUint32(e.buf[22:26], 0)

	if len(p.SegTable) > 255 {
		return errors.New("segment table too long")
	}
	e.buf[26] = byte(len(p.SegTable))

	pos := 27
	for i := 0; i < len(p.SegTable); i++ {
		e.buf[pos+i] = byte(p.SegTable[i])
	}
	pos += len(p.SegTable)

	p.Crc = crc32(e.buf[:pos])
	p.Crc = extendCrc32(p.Crc, p.Data)
	byteOrder.PutUint32(e.buf[22:26], p.Crc)

	_, err := e.b.Write(e.buf[:pos])
	if err != nil {
		return err
	}

	//todo: validate data size
	_, err = e.b.Write(p.Data)
	return err
}

//SplitPage splits one page into two identical pages but with data split at the segment(index goes to second page); if segmentIndex is out of bounds, one of the pages will have a zero length data
func SplitPage(p Page, segmentIndex int) (Page, Page) {
	if segmentIndex < 0 {
		segmentIndex = 0
	} else if segmentIndex > len(p.SegTable) {
		segmentIndex = len(p.SegTable)
	}

	var p2 = Page{
		Type:     p.Type,
		Granule:  p.Granule,
		Serial:   p.Serial,
		Sequence: p.Sequence,
		Crc:      p.Crc,
		SegTable: make([]int, 0),
		Data:     make([]byte, 0),
	}

	ds := 0
	for i := 0; i < len(p.SegTable) && i < segmentIndex; i++ {
		ds += p.SegTable[i]
	}

	//optimize if needed
	p2.SegTable = append(p2.SegTable, p.SegTable[segmentIndex:]...)
	p2.Data = append(p2.Data, p.Data[ds:]...)

	p.SegTable = p.SegTable[:segmentIndex]
	p.Data = p.Data[:ds]

	return p, p2
}

//ReadPacket reads data from page into packet buffer; fragment is a page of currently processed data (equal to p if packet doesn't finish on this page); remainder is a page with data that wasn't processed from p (can have an empty segment table and data)
func ReadPacket(p Page, packet *[]byte) (finished bool, fragment Page, remainder Page) {
	pb := -1
	for i, s := range p.SegTable {
		if s < 255 {
			finished = true
			pb = i
			break
		}
	}
	if pb < 0 {
		pb = math.MaxInt32
	}

	p1, remainder := SplitPage(p, pb+1)

	if packet == nil {
		*packet = make([]byte, len(p1.Data))
		copy(*packet, p1.Data)
	} else {
		*packet = append(*packet, p1.Data...)
	}

	return finished, p1, remainder
}

//WritePacket writes the packet and returns the last sequence number and an error.
//Warning: granule value is set according to vorbis, may not be valid for other formats
func WritePacket(f io.Writer, packet *[]byte, granule uint64, serial uint32, sequenceStart uint32, flags byte) (uint32, error) {
	var err error
	var enc = NewPageEncoder(f)

	var seqa uint32

	var dPos = 0
	var mstPos = 0
	var masterSegTbl = make([]int, (len(*packet)/mss)+1)
	for i := 0; i < len(masterSegTbl); i++ { //all segments besides the last one are always mss
		masterSegTbl[i] = mss
	}
	masterSegTbl[len(masterSegTbl)-1] = len(*packet) % mss

	for mstPos < len(masterSegTbl) { //write pages until all segments in mst are written
		p := Page{
			Type:     0,
			Granule:  18446744073709551615, //-1; not strictly ogg compliant, but is valid for vorbis
			Serial:   serial,
			Sequence: sequenceStart + seqa,
			Crc:      0,
			SegTable: nil,
			Data:     nil,
		}
		if mstPos != 0 {
			p.Type |= COP
		} else {
			if flags&BOS != 0 {
				p.Type |= BOS
			}
		}

		cstlen := 0
		for cstlen < 255 && mstPos+cstlen < len(masterSegTbl) {
			cstlen++
		}
		p.SegTable = masterSegTbl[mstPos : mstPos+cstlen]
		mstPos += cstlen

		//not strictly ogg granule setting
		if mstPos >= len(masterSegTbl) {
			p.Granule = granule
			if flags&EOS != 0 {
				p.Type |= EOS
			}
		}

		dLen := ((len(p.SegTable) * 255) - 255) + p.SegTable[len(p.SegTable)-1]
		p.Data = (*packet)[dPos : dPos+dLen]
		dPos += dLen

		err = enc.WritePage(p)
		if err != nil {
			return 0, err
		}

		seqa++
	}

	return sequenceStart + seqa, nil
}

//crc32 but starting at whatever the parameter is instead of 0
func extendCrc32(c uint32, d []byte) uint32 {
	for _, n := range d {
		c = crcTable[byte(c>>24)^n] ^ (c << 8)
	}
	return c
}

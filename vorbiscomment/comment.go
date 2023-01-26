package vorbiscomment

import (
	"encoding/binary"
	"errors"
	"github.com/ambeloe/oggv/ogg"
	"io"
)

type OggVorbis struct {
	headerPacket [3][]byte
	postPages    []ogg.Page //pages after 2nd packet with 2nd packet stripped out; page numbers will be fixed after

	VendorString string
	Comments     []string
}

// ReadOggVorbis create OggVorbis struct from read in file
func ReadOggVorbis(r io.Reader) (*OggVorbis, error) {
	var err error
	var oggv = OggVorbis{}

	var dec = ogg.NewPageDecoder(r)

	//read all pages
	packetNum := 0
	var p ogg.Page
	for {
		p, err = dec.ReadPage()
		switch err {
		case io.EOF, io.ErrUnexpectedEOF: //unexpected eof is weird; keep an eye on it
			goto ex
		case nil:
		default:
			return nil, err
		}
	redo:
		if packetNum < 3 {
			fin, _, rem := ogg.ReadPacket(p, &oggv.headerPacket[packetNum])
			if fin {
				p = rem
				packetNum++
				goto redo
			}
		} else { //don't care about packets past 3rd one
			oggv.postPages = append(oggv.postPages, p)
		}
	}
ex:

	//2nd packet is the comment header in vorbis
	if string(oggv.headerPacket[1][1:7]) != string([]byte{0x76, 0x6f, 0x72, 0x62, 0x69, 0x73}) { //"vorbis" magic value
		return nil, errors.New("second packet is not a vorbis header packet")
	} else if oggv.headerPacket[1][:1][0] != 3 { //first byte is header type; should be 3 for a comment field
		return nil, errors.New("header is not a comment header packet")
	}

	var pos uint32 = 7 //zero indexed array position
	ven_len := binary.LittleEndian.Uint32(oggv.headerPacket[1][pos : pos+4])
	pos += 4
	oggv.VendorString = string(oggv.headerPacket[1][pos : pos+ven_len])
	pos += ven_len
	oggv.Comments = make([]string, binary.LittleEndian.Uint32(oggv.headerPacket[1][pos:pos+4]))
	pos += 4
	for i := uint32(0); i < uint32(len(oggv.Comments)); i++ {
		taglen := binary.LittleEndian.Uint32(oggv.headerPacket[1][pos : pos+4])
		pos += 4

		oggv.Comments[i] = string(oggv.headerPacket[1][pos : pos+taglen])
		pos += taglen
	}

	//check for framing bit
	if oggv.headerPacket[1][len(oggv.headerPacket[1])-1] != 1 {
		return nil, errors.New("framing bit unset or missing")
	}

	return &oggv, nil
}

func WriteOggVorbis(w io.Writer, o *OggVorbis) error {
	var err error
	var seqs uint32

	updateCommentFieldPacket(o)

	var enc = ogg.NewPageEncoder(w)

	//18446744073709551615 == -1
	for i := 0; i < 3; i++ {
		if i == 0 {
			seqs, err = ogg.WritePacket(w, &o.headerPacket[i], 0, 0, seqs, ogg.BOS)
		} else {
			seqs, err = ogg.WritePacket(w, &o.headerPacket[i], 0, 0, seqs, 0)
		}
	}

	for _, p := range o.postPages {
		p.Sequence = seqs
		err = enc.WritePage(p)
		if err != nil {
			return err
		}
		seqs++
	}

	return nil
}

//regenerate comment field header from VendorString and Comments
func updateCommentFieldPacket(o *OggVorbis) {
	var temp = []byte{0, 0, 0, 0}

	var p = []byte{0x3, 0x76, 0x6f, 0x72, 0x62, 0x69, 0x73} //init with type and header already set

	//vendor string length and vendor string
	binary.LittleEndian.PutUint32(temp, uint32(len(o.VendorString)))
	p = append(p, temp...)
	p = append(p, []byte(o.VendorString)...)

	binary.LittleEndian.PutUint32(temp, uint32(len(o.Comments)))
	p = append(p, temp...)
	for _, r := range o.Comments {
		binary.LittleEndian.PutUint32(temp, uint32(len(r)))
		p = append(p, temp...)
		p = append(p, r...)
	}
	p = append(p, 1)

	o.headerPacket[1] = p
}

package vorbiscomment

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
)

//GenerateImageComment generates a vorbis comment string from an image file. imgType is a picture type according to ID3v2. Does not check that the image is valid for the picture type(32x32 icon). https://xiph.org/flac/format.html#metadata_block_picture
func GenerateImageComment(img []byte, imgDescription string, imgType uint32) (string, error) {
	var conf image.Config
	var temp, mbp []byte
	var err error

	mimeType := http.DetectContentType(img)

	switch mimeType {
	case "image/jpeg":
		conf, err = jpeg.DecodeConfig(bytes.NewReader(img))
	case "image/png":
		conf, err = png.DecodeConfig(bytes.NewReader(img))
	default:
		return "", errors.New("not a recognized image format")
	}
	if err != nil {
		return "", err
	}

	temp = make([]byte, 4)
	//generate unencoded frame
	binary.LittleEndian.PutUint32(temp, imgType)
	mbp = append(mbp, temp...)

	binary.LittleEndian.PutUint32(temp, uint32(len(mimeType)))
	mbp = append(mbp, temp...)

	mbp = append(mbp, mimeType...)

	binary.LittleEndian.PutUint32(temp, uint32(len(imgDescription)))
	mbp = append(mbp, temp...)

	mbp = append(mbp, imgDescription...)

	binary.LittleEndian.PutUint32(temp, uint32(conf.Width))
	mbp = append(mbp, temp...)

	binary.LittleEndian.PutUint32(temp, uint32(conf.Width))
	mbp = append(mbp, temp...)

	binary.LittleEndian.PutUint32(temp, uint32(8)) //todo: some images won't be 8bpp (png)
	mbp = append(mbp, temp...)

	binary.LittleEndian.PutUint32(temp, uint32(0))
	mbp = append(mbp, temp...)

	binary.LittleEndian.PutUint32(temp, uint32(len(img)))
	mbp = append(mbp, temp...)

	mbp = append(mbp, img...)

	//base64 for comment block
	return "METADATA_BLOCK_PICTURE=" + base64.StdEncoding.EncodeToString(mbp), nil
}

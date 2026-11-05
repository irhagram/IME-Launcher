// mkico converts frontend/logo.webp into build/icon.ico with 16..256px entries.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

var sizes = []int{16, 24, 32, 48, 64, 128, 256}

func main() {
	f, err := os.Open("frontend/logo.webp")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	src, err := webp.Decode(f)
	if err != nil {
		panic(err)
	}

	var pngs [][]byte
	for _, n := range sizes {
		dst := image.NewNRGBA(image.Rect(0, 0, n, n))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
		var b bytes.Buffer
		if err := png.Encode(&b, dst); err != nil {
			panic(err)
		}
		pngs = append(pngs, b.Bytes())
	}

	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, n := range sizes {
		w := byte(n)
		if n >= 256 {
			w = 0
		}
		ico.Write([]byte{w, w, 0, 0})
		binary.Write(&ico, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&ico, binary.LittleEndian, uint32(len(pngs[i])))
		binary.Write(&ico, binary.LittleEndian, uint32(offset))
		offset += len(pngs[i])
	}
	for _, p := range pngs {
		ico.Write(p)
	}
	os.MkdirAll("build", 0o755)
	if err := os.WriteFile("build/icon.ico", ico.Bytes(), 0o644); err != nil {
		panic(err)
	}
}

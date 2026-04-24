//go:build !goexperiment.simd || !amd64

package dziproxylib

func halfPlane(dst, src []uint8, dstW, dstH, srcStride int) {
	for y := 0; y < dstH; y++ {
		row0 := src[2*y*srcStride:]
		row1 := src[(2*y+1)*srcStride:]
		dstRow := dst[y*dstW:]
		for x := 0; x < dstW; x++ {
			dstRow[x] = uint8((uint16(row0[2*x]) + uint16(row0[2*x+1]) +
				uint16(row1[2*x]) + uint16(row1[2*x+1]) + 2) / 4)
		}
	}
}

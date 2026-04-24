//go:build goexperiment.simd && amd64

package dziproxylib

import "simd/archsimd"

var (
	simdEvenIdx = archsimd.LoadInt8x16(&[16]int8{0, 2, 4, 6, 8, 10, 12, 14, -1, -1, -1, -1, -1, -1, -1, -1})
	simdOddIdx  = archsimd.LoadInt8x16(&[16]int8{1, 3, 5, 7, 9, 11, 13, 15, -1, -1, -1, -1, -1, -1, -1, -1})
)

func halfPlane(dst, src []uint8, dstW, dstH, srcStride int) {
	simdW := dstW &^ 7
	for y := 0; y < dstH; y++ {
		row0 := src[2*y*srcStride:]
		row1 := src[(2*y+1)*srcStride:]
		dstRow := dst[y*dstW:]

		safeSimdW := simdW
		if y == dstH-1 && simdW > 0 {
			safeSimdW = simdW - 8
		}

		x := 0
		for ; x < safeSimdW; x += 8 {
			r0 := archsimd.LoadUint8x16Slice(row0[2*x:])
			avg0 := r0.PermuteOrZero(simdEvenIdx).Average(r0.PermuteOrZero(simdOddIdx))
			r1 := archsimd.LoadUint8x16Slice(row1[2*x:])
			avg1 := r1.PermuteOrZero(simdEvenIdx).Average(r1.PermuteOrZero(simdOddIdx))
			avg0.Average(avg1).StoreSlice(dstRow[x:])
		}

		for ; x < dstW; x++ {
			dstRow[x] = uint8((uint16(row0[2*x]) + uint16(row0[2*x+1]) +
				uint16(row1[2*x]) + uint16(row1[2*x+1]) + 2) / 4)
		}
	}
}

package dziproxylib

import "image"

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

func prepareSourceForScale(src image.Image, srcRect image.Rectangle, dstSize image.Point) (image.Image, image.Rectangle) {
	if sub, ok := src.(subImager); ok && srcRect != src.Bounds() {
		src = sub.SubImage(srcRect)
		srcRect = src.Bounds()
	}

	src = progressiveDownscale(src, dstSize.X, dstSize.Y)
	return src, src.Bounds()
}

func progressiveDownscale(src image.Image, targetW, targetH int) image.Image {
	if targetW <= 0 || targetH <= 0 {
		return src
	}

	for {
		bounds := src.Bounds()
		if bounds.Dx() <= 2*targetW || bounds.Dy() <= 2*targetH {
			return src
		}

		next, ok := downscaleHalf(src)
		if !ok {
			return src
		}
		src = next
	}
}

func downscaleHalf(src image.Image) (image.Image, bool) {
	switch img := src.(type) {
	case *image.YCbCr:
		return downscaleHalfYCbCr(img), true
	case *image.Gray:
		return downscaleHalfGray(img), true
	default:
		return nil, false
	}
}

func downscaleHalfYCbCr(src *image.YCbCr) image.Image {
	srcBounds := src.Bounds()
	dstW := srcBounds.Dx() / 2
	dstH := srcBounds.Dy() / 2
	if dstW == 0 || dstH == 0 {
		return src
	}

	dst := image.NewYCbCr(image.Rect(0, 0, dstW, dstH), src.SubsampleRatio)
	halfPlane(dst.Y, src.Y, dstW, dstH, src.YStride)

	dstChromaH := len(dst.Cb) / dst.CStride
	halfPlane(dst.Cb, src.Cb, dst.CStride, dstChromaH, src.CStride)
	halfPlane(dst.Cr, src.Cr, dst.CStride, dstChromaH, src.CStride)

	return dst
}

func downscaleHalfGray(src *image.Gray) image.Image {
	srcBounds := src.Bounds()
	dstW := srcBounds.Dx() / 2
	dstH := srcBounds.Dy() / 2
	if dstW == 0 || dstH == 0 {
		return src
	}

	dst := image.NewGray(image.Rect(0, 0, dstW, dstH))
	halfPlane(dst.Pix, src.Pix, dstW, dstH, src.Stride)
	return dst
}

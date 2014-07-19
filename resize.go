/*
Copyright (c) 2012, Jan Schlicht <jan.schlicht@gmail.com>

Permission to use, copy, modify, and/or distribute this software for any purpose
with or without fee is hereby granted, provided that the above copyright notice
and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS
OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
*/

// Package resize implements various image resizing methods.
//
// The package works with the Image interface described in the image package.
// Various interpolation methods are provided and multiple processors may be
// utilized in the computations.
//
// Example:
//     imgResized := resize.Resize(1000, 0, imgOld, resize.MitchellNetravali)
package resize

import (
	"image"
	"runtime"
	"sync"
)

// An InterpolationFunction provides the parameters that describe an
// interpolation kernel. It returns the number of samples to take
// and the kernel function to use for sampling.
type InterpolationFunction func() (int, func(float64) float64)

// Nearest-neighbor interpolation
func NearestNeighbor() (int, func(float64) float64) {
	return 2, nearest
}

// Bilinear interpolation
func Bilinear() (int, func(float64) float64) {
	return 2, linear
}

// Bicubic interpolation (with cubic hermite spline)
func Bicubic() (int, func(float64) float64) {
	return 4, cubic
}

// Mitchell-Netravali interpolation
func MitchellNetravali() (int, func(float64) float64) {
	return 4, mitchellnetravali
}

// Lanczos interpolation (a=2)
func Lanczos2() (int, func(float64) float64) {
	return 4, lanczos2
}

// Lanczos interpolation (a=3)
func Lanczos3() (int, func(float64) float64) {
	return 6, lanczos3
}

// values <1 will sharpen the image
var blur = 1.0

// Resize scales an image to new width and height using the interpolation function interp.
// A new image with the given dimensions will be returned.
// If one of the parameters width or height is set to 0, its size will be calculated so that
// the aspect ratio is that of the originating image.
// The resizing algorithm uses channels for parallel computation.
func Resize(width, height uint, img image.Image, interp InterpolationFunction) image.Image {
	scaleX, scaleY := calcFactors(width, height, float64(img.Bounds().Dx()), float64(img.Bounds().Dy()))
	if width == 0 {
		width = uint(0.7 + float64(img.Bounds().Dx())/scaleX)
	}
	if height == 0 {
		height = uint(0.7 + float64(img.Bounds().Dy())/scaleY)
	}

	taps, kernel := interp()
	cpus := runtime.NumCPU()
	wg := sync.WaitGroup{}

	// Generic access to image.Image is slow in tight loops.
	// The optimal access has to be determined from the concrete image type.
	switch input := img.(type) {
	case *image.RGBA:
		// 8-bit precision
		temp := image.NewRGBA(image.Rect(0, 0, input.Bounds().Dy(), int(width)))
		result := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))

		// horizontal filter, results in transposed temporary image
		coeffs, filterLength := createWeights8(temp.Bounds().Dy(), input.Bounds().Min.X, taps, blur, scaleX, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(temp, i, cpus).(*image.RGBA)
			go func() {
				defer wg.Done()
				resizeRGBA(input, slice, scaleX, coeffs, filterLength)
			}()
		}
		wg.Wait()

		// horizontal filter on transposed image, result is not transposed
		coeffs, filterLength = createWeights8(result.Bounds().Dy(), temp.Bounds().Min.X, taps, blur, scaleY, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(result, i, cpus).(*image.RGBA)
			go func() {
				defer wg.Done()
				resizeRGBA(temp, slice, scaleY, coeffs, filterLength)
			}()
		}
		wg.Wait()
		return result
	case *image.YCbCr:
		// 8-bit precision
		// accessing the YCbCr arrays in a tight loop is slow.
		// converting the image before filtering will improve performance.
		inputAsRGBA := convertYCbCrToRGBA(input)
		temp := image.NewRGBA(image.Rect(0, 0, input.Bounds().Dy(), int(width)))
		result := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))

		// horizontal filter, results in transposed temporary image
		coeffs, filterLength := createWeights8(temp.Bounds().Dy(), input.Bounds().Min.X, taps, blur, scaleX, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(temp, i, cpus).(*image.RGBA)
			go func() {
				defer wg.Done()
				resizeRGBA(inputAsRGBA, slice, scaleX, coeffs, filterLength)
			}()
		}
		wg.Wait()

		// horizontal filter on transposed image, result is not transposed
		coeffs, filterLength = createWeights8(result.Bounds().Dy(), temp.Bounds().Min.X, taps, blur, scaleY, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(result, i, cpus).(*image.RGBA)
			go func() {
				defer wg.Done()
				resizeRGBA(temp, slice, scaleY, coeffs, filterLength)
			}()
		}
		wg.Wait()
		return result
	case *image.RGBA64:
		// 16-bit precision
		temp := image.NewRGBA64(image.Rect(0, 0, input.Bounds().Dy(), int(width)))
		result := image.NewRGBA64(image.Rect(0, 0, int(width), int(height)))

		// horizontal filter, results in transposed temporary image
		coeffs, filterLength := createWeights16(temp.Bounds().Dy(), input.Bounds().Min.X, taps, blur, scaleX, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(temp, i, cpus).(*image.RGBA64)
			go func() {
				defer wg.Done()
				resizeRGBA64(input, slice, scaleX, coeffs, filterLength)
			}()
		}
		wg.Wait()

		// horizontal filter on transposed image, result is not transposed
		coeffs, filterLength = createWeights16(result.Bounds().Dy(), temp.Bounds().Min.X, taps, blur, scaleY, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(result, i, cpus).(*image.RGBA64)
			go func() {
				defer wg.Done()
				resizeGeneric(temp, slice, scaleY, coeffs, filterLength)
			}()
		}
		wg.Wait()
		return result
	case *image.Gray:
		// 8-bit precision
		temp := image.NewGray(image.Rect(0, 0, input.Bounds().Dy(), int(width)))
		result := image.NewGray(image.Rect(0, 0, int(width), int(height)))

		// horizontal filter, results in transposed temporary image
		coeffs, filterLength := createWeights8(temp.Bounds().Dy(), input.Bounds().Min.X, taps, blur, scaleX, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(temp, i, cpus).(*image.Gray)
			go func() {
				defer wg.Done()
				resizeGray(input, slice, scaleX, coeffs, filterLength)
			}()
		}
		wg.Wait()

		// horizontal filter on transposed image, result is not transposed
		coeffs, filterLength = createWeights8(result.Bounds().Dy(), temp.Bounds().Min.X, taps, blur, scaleY, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(result, i, cpus).(*image.Gray)
			go func() {
				defer wg.Done()
				resizeGray(temp, slice, scaleY, coeffs, filterLength)
			}()
		}
		wg.Wait()
		return result
	case *image.Gray16:
		// 16-bit precision
		temp := image.NewGray16(image.Rect(0, 0, input.Bounds().Dy(), int(width)))
		result := image.NewGray16(image.Rect(0, 0, int(width), int(height)))

		// horizontal filter, results in transposed temporary image
		coeffs, filterLength := createWeights16(temp.Bounds().Dy(), input.Bounds().Min.X, taps, blur, scaleX, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(temp, i, cpus).(*image.Gray16)
			go func() {
				defer wg.Done()
				resizeGray16(input, slice, scaleX, coeffs, filterLength)
			}()
		}
		wg.Wait()

		// horizontal filter on transposed image, result is not transposed
		coeffs, filterLength = createWeights16(result.Bounds().Dy(), temp.Bounds().Min.X, taps, blur, scaleY, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(result, i, cpus).(*image.Gray16)
			go func() {
				defer wg.Done()
				resizeGray16(temp, slice, scaleY, coeffs, filterLength)
			}()
		}
		wg.Wait()
		return result
	default:
		// 16-bit precision
		temp := image.NewRGBA64(image.Rect(0, 0, img.Bounds().Dy(), int(width)))
		result := image.NewRGBA64(image.Rect(0, 0, int(width), int(height)))

		// horizontal filter, results in transposed temporary image
		coeffs, filterLength := createWeights16(temp.Bounds().Dy(), img.Bounds().Min.X, taps, blur, scaleX, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(temp, i, cpus).(*image.RGBA64)
			go func() {
				defer wg.Done()
				resizeGeneric(img, slice, scaleX, coeffs, filterLength)
			}()
		}
		wg.Wait()

		// horizontal filter on transposed image, result is not transposed
		coeffs, filterLength = createWeights16(result.Bounds().Dy(), temp.Bounds().Min.X, taps, blur, scaleY, kernel)
		wg.Add(cpus)
		for i := 0; i < cpus; i++ {
			slice := makeSlice(result, i, cpus).(*image.RGBA64)
			go func() {
				defer wg.Done()
				resizeRGBA64(temp, slice, scaleY, coeffs, filterLength)
			}()
		}
		wg.Wait()
		return result
	}
}

// Calculates scaling factors using old and new image dimensions.
func calcFactors(width, height uint, oldWidth, oldHeight float64) (scaleX, scaleY float64) {
	if width == 0 {
		if height == 0 {
			scaleX = 1.0
			scaleY = 1.0
		} else {
			scaleY = oldHeight / float64(height)
			scaleX = scaleY
		}
	} else {
		scaleX = oldWidth / float64(width)
		if height == 0 {
			scaleY = scaleX
		} else {
			scaleY = oldHeight / float64(height)
		}
	}
	return
}

type imageWithSubImage interface {
	image.Image
	SubImage(image.Rectangle) image.Image
}

func makeSlice(img imageWithSubImage, i, n int) image.Image {
	return img.SubImage(image.Rect(img.Bounds().Min.X, img.Bounds().Min.Y+i*img.Bounds().Dy()/n, img.Bounds().Max.X, img.Bounds().Min.Y+(i+1)*img.Bounds().Dy()/n))
}

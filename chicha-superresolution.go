package main

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

// Main entry point for the server
func main() {
	// Register routes for the web interface
	http.HandleFunc("/", uploadPageHandler) // Render the upload page
	http.HandleFunc("/upload", uploadHandler) // Handle file uploads

	// Serve static files like Bootstrap CSS
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Start the HTTP server
	log.Println("Server running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Upload page handler: displays the HTML page for image uploads
func uploadPageHandler(w http.ResponseWriter, r *http.Request) {
	// HTML template for the upload page
	const uploadPageHTML = `
	<!DOCTYPE html>
	<html lang="en">
	<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Super Resolution</title>
	<link href="/static/bootstrap.min.css" rel="stylesheet">
	</head>
	<body class="bg-light">
	<div class="container py-5">
	<h1 class="mb-4 text-center text-primary">Super Resolution Tool</h1>
	<form action="/upload" method="post" enctype="multipart/form-data" class="bg-white p-4 rounded shadow">
	<div class="mb-3">
	<label for="images" class="form-label">Upload Images (JPEG only)</label>
	<input type="file" name="images" id="images" multiple required class="form-control">
	</div>
	<div class="d-grid gap-2">
	<button type="submit" class="btn btn-success btn-lg">Submit Images</button>
	</div>
	</form>
	</div>
	</body>
	</html>
	`
	// Serve the HTML template as a response
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(uploadPageHTML))
}

// uploadHandler processes uploaded images, validates their formats, and performs super-resolution if valid
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Parse uploaded files from the form
	err := r.ParseMultipartForm(10 << 20) // Allow up to 10 MB for the form data
	if err != nil {
		http.Error(w, "Unable to parse uploaded files", http.StatusBadRequest) // Send an error if parsing fails
		return
	}

	// Create a temporary directory to store uploaded images
	tempDir, err := os.MkdirTemp("", "superres") // Create a unique directory for this request
	if err != nil {
		http.Error(w, "Failed to create temporary directory", http.StatusInternalServerError) // Handle directory creation failure
		return
	}
	defer os.RemoveAll(tempDir) // Clean up the temporary directory after processing

	// Store paths of the uploaded images
	var imagePaths []string
	for _, fileHeader := range r.MultipartForm.File["images"] { // Iterate over each uploaded file
		// Open the uploaded file
		file, err := fileHeader.Open()
		if err != nil {
			http.Error(w, "Error opening uploaded file", http.StatusInternalServerError) // Send error if file cannot be opened
			return
		}
		defer file.Close() // Ensure the file is closed after processing

		// Save the file to the temporary directory
		destPath := filepath.Join(tempDir, fileHeader.Filename) // Construct the destination path
		destFile, err := os.Create(destPath)                   // Create a new file in the temp directory
		if err != nil {
			http.Error(w, "Error saving uploaded file", http.StatusInternalServerError) // Handle file saving errors
			return
		}
		defer destFile.Close() // Ensure the destination file is closed after writing

		// Copy the contents of the uploaded file to the destination
		_, err = io.Copy(destFile, file)
		if err != nil {
			http.Error(w, "Error copying file data", http.StatusInternalServerError) // Handle file copy errors
			return
		}

		// Add the file path to the list of image paths
		imagePaths = append(imagePaths, destPath)
	}

	// Decode and validate the uploaded images
	var images []image.Image // List to hold successfully decoded images
	for _, path := range imagePaths {
		// Open the saved image file
		file, err := os.Open(path)
		if err != nil {
			http.Error(w, "Error opening saved file", http.StatusInternalServerError) // Handle file open errors
			return
		}
		defer file.Close() // Ensure the file is closed after reading

		// Decode the image to check its format
		img, format, err := image.Decode(file)
		if err != nil {
			// If decoding fails, send an error with the list of supported formats
			supportedFormats := "JPEG, PNG, GIF"
			http.Error(w, fmt.Sprintf("Unsupported format for file %s. Supported formats are: %s", filepath.Base(path), supportedFormats), http.StatusBadRequest)
			return
		}
		log.Printf("Decoded %s as %s format", path, format) // Log the successful decoding

		// Add the successfully decoded image to the list
		images = append(images, img)
	}

	// Ensure there are valid images to process
	if len(images) == 0 {
		http.Error(w, "No valid images to process. Please upload supported formats only.", http.StatusBadRequest) // Send error if no valid images
		return
	}

	// Calculate the maximum scaling factor based on the number of valid images
	maxScale := int(math.Sqrt(float64(len(images)))) // Use the square root of the image count as the scaling factor
	log.Printf("Maximum scaling factor determined: %dx", maxScale)

	// Perform super-resolution
	result := performSuperResolution(images, maxScale) // Call the function to generate the high-resolution image

	// Return the resulting image to the client
	w.Header().Set("Content-Type", "image/jpeg") // Set the content type to JPEG
	err = jpeg.Encode(w, result, nil)           // Encode the resulting image to JPEG and write it to the response
	if err != nil {
		http.Error(w, "Error encoding high-resolution image", http.StatusInternalServerError) // Handle encoding errors
	}
}

// performSuperResolution takes images and a scaling factor to generate a high-resolution image
func performSuperResolution(images []image.Image, upscaleFactor int) *image.RGBA {
	// Determine the dimensions of the input images
	srcBounds := images[0].Bounds() // Use the bounds of the first image as a reference
	highResWidth := srcBounds.Dx() * upscaleFactor  // Calculate the width of the high-resolution output
	highResHeight := srcBounds.Dy() * upscaleFactor // Calculate the height of the high-resolution output

	// Align the images
	alignedImages := alignImages(images)

	// Create arrays to accumulate RGB values and weights
	accR := make([][]float64, highResHeight)    // Accumulated red channel values
	accG := make([][]float64, highResHeight)    // Accumulated green channel values
	accB := make([][]float64, highResHeight)    // Accumulated blue channel values
	weights := make([][]float64, highResHeight) // Weights for normalization

	// Initialize the arrays for accumulation and weights
	for y := range accR {
		accR[y] = make([]float64, highResWidth)
		accG[y] = make([]float64, highResWidth)
		accB[y] = make([]float64, highResWidth)
		weights[y] = make([]float64, highResWidth)
	}

	// Accumulate pixel data from all images
	for _, img := range alignedImages {
		// Create a high-resolution version of the image
		highResImgTmp := image.NewRGBA(image.Rect(0, 0, highResWidth, highResHeight))
		// Scale the image into the high-resolution image using bilinear interpolation
		draw.BiLinear.Scale(highResImgTmp, highResImgTmp.Bounds(), img, img.Bounds(), draw.Over, nil)

		// Now accumulate the pixel data
		for y := 0; y < highResHeight; y++ {
			for x := 0; x < highResWidth; x++ {
				r, g, b, _ := highResImgTmp.At(x, y).RGBA()
				accR[y][x] += float64(r >> 8)
				accG[y][x] += float64(g >> 8)
				accB[y][x] += float64(b >> 8)
				weights[y][x]++
			}
		}
	}

	// Create an empty RGBA image for the high-resolution output
	highResImg := image.NewRGBA(image.Rect(0, 0, highResWidth, highResHeight))

	// Normalize accumulated values and generate the final high-resolution image
	for y := 0; y < highResHeight; y++ {
		for x := 0; x < highResWidth; x++ {
			if weights[y][x] > 0 {
				// Normalize and clamp the values
				r := uint8(math.Min(math.Round(accR[y][x]/weights[y][x]), 255))
				g := uint8(math.Min(math.Round(accG[y][x]/weights[y][x]), 255))
				b := uint8(math.Min(math.Round(accB[y][x]/weights[y][x]), 255))
				highResImg.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
			} else {
				// Set a default color (white)
				highResImg.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}

	return highResImg // Return the final high-resolution image
}

// alignImages aligns a list of images based on the first image
func alignImages(images []image.Image) []image.Image {
	reference := images[0] // Use the first image as the reference
	alignedImages := []image.Image{reference}

	for i := 1; i < len(images); i++ {
		img := images[i]
		dx, dy := estimateTranslation(reference, img)
		alignedImg := shiftImage(img, dx, dy)
		alignedImages = append(alignedImages, alignedImg)
	}

	return alignedImages
}

// estimateTranslation estimates the shift (dx, dy) between two images
func estimateTranslation(refImg, img image.Image) (dx, dy int) {
	// Define the maximum shift to search
	maxShift := 10 // pixels

	minSSD := math.MaxFloat64
	bestDx, bestDy := 0, 0

	for yShift := -maxShift; yShift <= maxShift; yShift++ {
		for xShift := -maxShift; xShift <= maxShift; xShift++ {
			ssd := computeSSD(refImg, img, xShift, yShift)
			if ssd < minSSD {
				minSSD = ssd
				bestDx = xShift
				bestDy = yShift
			}
		}
	}

	return bestDx, bestDy
}

// computeSSD computes the Sum of Squared Differences between two images with a given shift
func computeSSD(refImg, img image.Image, xShift, yShift int) float64 {
	ssd := 0.0
	bounds := refImg.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			refX := x
			refY := y
			imgX := x + xShift
			imgY := y + yShift

			if imgX < bounds.Min.X || imgX >= bounds.Max.X || imgY < bounds.Min.Y || imgY >= bounds.Max.Y {
				continue
			}

			refR, refG, refB, _ := refImg.At(refX, refY).RGBA()
			imgR, imgG, imgB, _ := img.At(imgX, imgY).RGBA()

			dr := float64((refR >> 8) - (imgR >> 8))
			dg := float64((refG >> 8) - (imgG >> 8))
			db := float64((refB >> 8) - (imgB >> 8))

			ssd += dr*dr + dg*dg + db*db
		}
	}
	return ssd
}

// shiftImage shifts an image by dx and dy pixels
func shiftImage(img image.Image, dx, dy int) image.Image {
	bounds := img.Bounds()
	shiftedImg := image.NewRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			srcX := x - dx
			srcY := y - dy

			if srcX < bounds.Min.X || srcX >= bounds.Max.X || srcY < bounds.Min.Y || srcY >= bounds.Max.Y {
				shiftedImg.Set(x, y, color.Black)
			} else {
				shiftedImg.Set(x, y, img.At(srcX, srcY))
			}
		}
	}

	return shiftedImg
}

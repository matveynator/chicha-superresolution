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
	result := performSuperResolution(imagePaths, maxScale) // Call the function to generate the high-resolution image

	// Return the resulting image to the client
	w.Header().Set("Content-Type", "image/jpeg") // Set the content type to JPEG
	err = jpeg.Encode(w, result, nil)           // Encode the resulting image to JPEG and write it to the response
	if err != nil {
		http.Error(w, "Error encoding high-resolution image", http.StatusInternalServerError) // Handle encoding errors
	}
}


// performSuperResolution takes image file paths and a scaling factor to generate a high-resolution image
func performSuperResolution(imagePaths []string, upscaleFactor int) *image.RGBA {
	// Open and decode all images
	var images []image.Image // A slice to store decoded images
	for _, path := range imagePaths {
		// Open the image file
		file, err := os.Open(path) // Open the file for reading
		if err != nil {
			log.Fatalf("Error opening file %s: %v", path, err) // Log and terminate if the file cannot be opened
		}
		defer file.Close() // Ensure the file is closed after processing

		// Decode the image to determine format
		img, _, err := image.Decode(file)
		if err != nil {
			log.Fatalf("Error decoding file %s: %v", path, err) // Log and terminate if decoding fails
		}
		images = append(images, img) // Add the decoded image to the slice
	}

	// Determine the dimensions of the input images
	srcBounds := images[0].Bounds() // Use the bounds of the first image as a reference
	highResWidth := srcBounds.Dx() * upscaleFactor  // Calculate the width of the high-resolution output
	highResHeight := srcBounds.Dy() * upscaleFactor // Calculate the height of the high-resolution output

	// Create an empty RGBA image for the high-resolution output
	highResImg := image.NewRGBA(image.Rect(0, 0, highResWidth, highResHeight))

	// Arrays to accumulate RGB values and weights
	accR := make([][]float64, highResHeight) // Array to store accumulated red channel values
	accG := make([][]float64, highResHeight) // Array to store accumulated green channel values
	accB := make([][]float64, highResHeight) // Array to store accumulated blue channel values
	weights := make([][]float64, highResHeight) // Array to store weights for normalization

	// Initialize the arrays for accumulation and weights
	for y := range accR {
		accR[y] = make([]float64, highResWidth) // Initialize the red channel array for this row
		accG[y] = make([]float64, highResWidth) // Initialize the green channel array for this row
		accB[y] = make([]float64, highResWidth) // Initialize the blue channel array for this row
		weights[y] = make([]float64, highResWidth) // Initialize the weights array for this row
	}

	// Accumulate pixel data from all images
	for _, img := range images {
		// Iterate over the original image pixels
		for y := 0; y < srcBounds.Dy(); y++ {
			for x := 0; x < srcBounds.Dx(); x++ {
				// Map the pixel to high-resolution coordinates
				hrX := x * upscaleFactor // High-resolution X coordinate
				hrY := y * upscaleFactor // High-resolution Y coordinate

				// Get RGB values from the original image (scaled to 8-bit)
				r, g, b, _ := img.At(x, y).RGBA() // Extract RGBA values (16-bit)
				accR[hrY][hrX] += float64(r >> 8) // Accumulate red channel (convert to 8-bit)
				accG[hrY][hrX] += float64(g >> 8) // Accumulate green channel (convert to 8-bit)
				accB[hrY][hrX] += float64(b >> 8) // Accumulate blue channel (convert to 8-bit)
				weights[hrY][hrX]++               // Increment the weight for normalization
			}
		}
	}

	// Normalize accumulated values and generate the final high-resolution image
	for y := 0; y < highResHeight; y++ {
		for x := 0; x < highResWidth; x++ {
			if weights[y][x] > 0 { // Normalize only if there are contributions to this pixel
				// Normalize the accumulated values to calculate the final pixel value
				r := uint8(math.Min(math.Round(accR[y][x]/weights[y][x]), 255)) // Normalize red and clamp to 255
				g := uint8(math.Min(math.Round(accG[y][x]/weights[y][x]), 255)) // Normalize green and clamp to 255
				b := uint8(math.Min(math.Round(accB[y][x]/weights[y][x]), 255)) // Normalize blue and clamp to 255
				highResImg.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})  // Set the calculated RGBA value
			} else {
				// If no contributions, set a default color (white) to prevent black pixels
				highResImg.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}

	return highResImg // Return the final high-resolution image
}


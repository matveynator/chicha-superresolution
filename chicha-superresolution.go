package main

import (
	_ "embed" // Required for embedding
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
	"runtime"
	"sync"

	"golang.org/x/image/draw"
)

//go:embed static/bootstrap.min.css
var bootstrapCSS string

// Main entry point for the server
func main() {
	// Register routes for the web interface
	http.HandleFunc("/", uploadPageHandler)   // Render the upload page
	http.HandleFunc("/upload", uploadHandler) // Handle file uploads

	// Start the HTTP server
	log.Println("Server running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func uploadPageHandler(w http.ResponseWriter, r *http.Request) {
	// Serve the HTML template with embedded CSS
	const uploadPageHTML = `
	<!DOCTYPE html>
	<html lang="en">
	<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>Super Resolution</title>
	<style>%s</style>
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
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, uploadPageHTML, bootstrapCSS)
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
		destFile, err := os.Create(destPath)                    // Create a new file in the temp directory
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
	err = jpeg.Encode(w, result, nil)            // Encode the resulting image to JPEG and write it to the response
	if err != nil {
		http.Error(w, "Error encoding high-resolution image", http.StatusInternalServerError) // Handle encoding errors
	}
}

// performSuperResolution реализует суперразрешение с параллелизмом
func performSuperResolution(images []image.Image, upscaleFactor int) *image.RGBA {
	log.Println("Starting super-resolution process...")

	srcBounds := images[0].Bounds()
	highResWidth := srcBounds.Dx() * upscaleFactor
	highResHeight := srcBounds.Dy() * upscaleFactor

	// Параллельное выравнивание изображений
	log.Println("Aligning images before processing...")
	alignedImages := findAndAlignImages(images)

	// Инициализация матриц для накопления
	accR := make([][]float64, highResHeight)
	accG := make([][]float64, highResHeight)
	accB := make([][]float64, highResHeight)
	weights := make([][]float64, highResHeight)
	for y := range accR {
		accR[y] = make([]float64, highResWidth)
		accG[y] = make([]float64, highResWidth)
		accB[y] = make([]float64, highResWidth)
		weights[y] = make([]float64, highResWidth)
	}

	// Канал для параллельной обработки пикселей
	taskChan := make(chan *image.RGBA, len(alignedImages))
	var wg sync.WaitGroup

	numCPUs := runtime.NumCPU()
	log.Printf("Using %d CPU cores for pixel accumulation...", numCPUs)

	// Горутины для обработки пикселей
	for i := 0; i < numCPUs; i++ {
		go func() {
			for img := range taskChan {
				for y := 0; y < highResHeight; y++ {
					for x := 0; x < highResWidth; x++ {
						r, g, b, _ := img.At(x, y).RGBA()
						accR[y][x] += float64(r >> 8)
						accG[y][x] += float64(g >> 8)
						accB[y][x] += float64(b >> 8)
						weights[y][x]++
					}
				}
				wg.Done()
			}
		}()
	}

	// Масштабирование изображений и отправка в канал
	for _, img := range alignedImages {
		wg.Add(1)
		highResImgTmp := image.NewRGBA(image.Rect(0, 0, highResWidth, highResHeight))
		draw.BiLinear.Scale(highResImgTmp, highResImgTmp.Bounds(), img, img.Bounds(), draw.Over, nil)
		taskChan <- highResImgTmp
	}

	close(taskChan)
	wg.Wait()

	// Генерация итогового изображения
	log.Println("Combining accumulated data into the final high-resolution image...")
	highResImg := image.NewRGBA(image.Rect(0, 0, highResWidth, highResHeight))
	for y := 0; y < highResHeight; y++ {
		for x := 0; x < highResWidth; x++ {
			if weights[y][x] > 0 {
				r := uint8(math.Min(math.Round(accR[y][x]/weights[y][x]), 255))
				g := uint8(math.Min(math.Round(accG[y][x]/weights[y][x]), 255))
				b := uint8(math.Min(math.Round(accB[y][x]/weights[y][x]), 255))
				highResImg.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
			} else {
				highResImg.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}

	log.Println("Super-resolution process completed successfully.")
	return highResImg
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

func findAndAlignImages(images []image.Image) []image.Image {
	log.Println("Starting parallel image alignment process...")
	reference := images[0] // Опорное изображение
	alignedImages := make([]image.Image, len(images))
	alignedImages[0] = reference // Первое изображение уже выровнено

	var wg sync.WaitGroup
	for i := 1; i < len(images); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			img := images[i]
			log.Printf("Aligning image %d with the reference image...", i)

			// Найти оптимальное совмещение
			dx, dy := findOverlap(reference, img)
			log.Printf("Optimal shift for image %d: dx=%d, dy=%d", i, dx, dy)

			// Сдвинуть текущее изображение
			alignedImages[i] = shiftImage(img, dx, dy)
		}(i)
	}

	// Ожидание завершения всех горутин
	wg.Wait()
	log.Println("Image alignment process completed.")
	return alignedImages
}


func findOverlap(refImg, img image.Image) (dx, dy int) {
	log.Println("Starting parallel overlap calculation...")
	maxShift := 50 // Максимальное смещение (в пикселях)
	type result struct {
		xShift, yShift int
		diff           float64
	}
	resultsChan := make(chan result, (2*maxShift+1)*(2*maxShift+1))
	var wg sync.WaitGroup

	// Параллелизация расчётов для всех комбинаций смещений
	for yShift := -maxShift; yShift <= maxShift; yShift++ {
		for xShift := -maxShift; xShift <= maxShift; xShift++ {
			wg.Add(1)
			go func(x, y int) {
				defer wg.Done()
				diff := calculateDifference(refImg, img, x, y)
				resultsChan <- result{xShift: x, yShift: y, diff: diff}
			}(xShift, yShift)
		}
	}

	// Закрываем канал после завершения всех горутин
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Поиск минимального значения
	minDiff := math.MaxFloat64
	for res := range resultsChan {
		if res.diff < minDiff {
			minDiff = res.diff
			dx = res.xShift
			dy = res.yShift
		}
	}

	log.Printf("Found optimal overlap: dx=%d, dy=%d, minDiff=%f", dx, dy, minDiff)
	return dx, dy
}



func calculateDifference(refImg, img image.Image, dx, dy int) float64 {
	// Логирование только для отладки; основной вывод будет в других функциях
	totalDiff := 0.0
	count := 0

	refBounds := refImg.Bounds()
	imgBounds := img.Bounds()

	for y := refBounds.Min.Y; y < refBounds.Max.Y; y++ {
		for x := refBounds.Min.X; x < refBounds.Max.X; x++ {
			imgX := x + dx
			imgY := y + dy

			if imgX < imgBounds.Min.X || imgX >= imgBounds.Max.X || imgY < imgBounds.Min.Y || imgY >= imgBounds.Max.Y {
				continue
			}

			refR, refG, refB, _ := refImg.At(x, y).RGBA()
			imgR, imgG, imgB, _ := img.At(imgX, imgY).RGBA()

			dr := float64((refR >> 8) - (imgR >> 8))
			dg := float64((refG >> 8) - (imgG >> 8))
			db := float64((refB >> 8) - (imgB >> 8))

			totalDiff += dr*dr + dg*dg + db*db
			count++
		}
	}

	if count == 0 {
		return math.MaxFloat64
	}
	return totalDiff / float64(count)
}

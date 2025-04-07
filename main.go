package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

type Config struct {
	Threshold      float64 `json:"threshold"`
	DivisionFactor float64 `json:"division_factor"`
	RestoreDelay   int     `json:"restore_delay"`
	Verbose        bool    `json:"verbose"`
	SampleWindow   int     `json:"sample_window"`
}

var defaultConfig = Config{
	Threshold:      0.4,
	DivisionFactor: 4.0,
	RestoreDelay:   3,
	Verbose:        false,
	SampleWindow:   5,
}

var (
	threshold      = flag.Float64("threshold", -1, "Audio peak threshold (0.0-1.0)")
	divisionFactor = flag.Float64("division", -1, "Factor to divide volume by when loud sound detected")
	restoreDelay   = flag.Int("delay", -1, "Seconds to wait before restoring volume")
	verbose        = flag.Bool("verbose", false, "Enable verbose output")
	configFile     = flag.String("config", "", "Path to configuration file")
)

func loadConfig(filePath string) (Config, error) {
	config := defaultConfig

	if filePath == "" {
		execPath, err := os.Executable()
		if err != nil {
			return config, fmt.Errorf("failed to get executable path: %v", err)
		}
		filePath = filepath.Join(filepath.Dir(execPath), "config.json")
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		configData, err := json.MarshalIndent(config, "", "  ")
		if err != nil {
			return config, fmt.Errorf("failed to marshal default config: %v", err)
		}
		if err := os.WriteFile(filePath, configData, 0644); err != nil {
			return config, fmt.Errorf("failed to write default config file: %v", err)
		}
		fmt.Printf("[%s] Created default configuration file at: %s\n", time.Now().Format("15:04:05"), filePath)
		return config, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return config, fmt.Errorf("failed to read config file: %v", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("failed to parse config file: %v", err)
	}

	fmt.Printf("[%s] Loaded configuration from: %s\n", time.Now().Format("15:04:05"), filePath)
	return config, nil
}

func main() {
	flag.Parse()

	config, err := loadConfig(*configFile)
	if err != nil {
		fmt.Printf("[%s] Warning: %v\n", time.Now().Format("15:04:05"), err)
		fmt.Printf("[%s] Using default configuration\n", time.Now().Format("15:04:05"))
	}

	if *threshold >= 0 {
		config.Threshold = *threshold
	}
	if *divisionFactor >= 0 {
		config.DivisionFactor = *divisionFactor
	}
	if *restoreDelay >= 0 {
		config.RestoreDelay = *restoreDelay
	}
	config.Verbose = *verbose

	fmt.Printf("[%s] Starting EarGuard...\n", time.Now().Format("15:04:05"))
	fmt.Printf("[%s] Settings: Threshold: %.2f, Volume division: 1/%.0f, Restore delay: %ds, Sample window: %d, Verbose: %v\n",
		time.Now().Format("15:04:05"), config.Threshold, config.DivisionFactor, config.RestoreDelay, config.SampleWindow, config.Verbose)

	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		log.Fatalf("Failed to initialize COM: %v", err)
	}
	defer ole.CoUninitialize()

	var deviceEnumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator,
		0,
		wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator,
		&deviceEnumerator,
	); err != nil {
		log.Fatalf("Failed to create device enumerator: %v", err)
	}
	defer deviceEnumerator.Release()

	var device *wca.IMMDevice
	if err := deviceEnumerator.GetDefaultAudioEndpoint(
		wca.ERender,
		wca.EConsole,
		&device,
	); err != nil {
		log.Fatalf("Failed to get default audio endpoint: %v", err)
	}
	defer device.Release()

	var audioMeter *wca.IAudioMeterInformation
	if err := device.Activate(
		wca.IID_IAudioMeterInformation,
		wca.CLSCTX_ALL,
		nil,
		&audioMeter,
	); err != nil {
		log.Fatalf("Failed to activate audio meter: %v", err)
	}
	defer audioMeter.Release()

	var audioEndpointVolume *wca.IAudioEndpointVolume
	if err := device.Activate(
		wca.IID_IAudioEndpointVolume,
		wca.CLSCTX_ALL,
		nil,
		&audioEndpointVolume,
	); err != nil {
		log.Fatalf("Failed to activate audio endpoint volume: %v", err)
	}
	defer audioEndpointVolume.Release()

	fmt.Printf("[%s] Audio monitoring active - Press Ctrl+C to exit\n", time.Now().Format("15:04:05"))

	var isReducedVolume bool = false
	var originalVolume float32
	var lastLoudTimestamp time.Time
	var lastPrintTime time.Time = time.Now()

	peakBuffer := make([]float32, config.SampleWindow)
	bufferIndex := 0

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		fmt.Printf("[%s] Shutting down...\n", time.Now().Format("15:04:05"))
		if isReducedVolume {
			fmt.Printf("[%s] Restoring volume...\n", time.Now().Format("15:04:05"))
			audioEndpointVolume.SetMasterVolumeLevelScalar(originalVolume, nil)
		}
		os.Exit(0)
	}()

	for {
		var peakValue float32
		if err := audioMeter.GetPeakValue(&peakValue); err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		peakBuffer[bufferIndex] = peakValue
		bufferIndex = (bufferIndex + 1) % config.SampleWindow

		var sumPeaks float32
		for _, p := range peakBuffer {
			sumPeaks += p
		}
		averagePeak := sumPeaks / float32(config.SampleWindow)

		var currentVolume float32
		if err := audioEndpointVolume.GetMasterVolumeLevelScalar(&currentVolume); err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if config.Verbose && time.Since(lastPrintTime) > 2*time.Second {
			fmt.Printf("[%s] Current peak: %.2f, Avg peak: %.2f, Volume: %.0f%%, Reduced: %v\n",
				time.Now().Format("15:04:05"), peakValue, averagePeak, currentVolume*100, isReducedVolume)
			lastPrintTime = time.Now()
		}

		if averagePeak > float32(config.Threshold) && !isReducedVolume {
			originalVolume = currentVolume
			newVolume := originalVolume / float32(config.DivisionFactor)
			if err := audioEndpointVolume.SetMasterVolumeLevelScalar(newVolume, nil); err != nil {
				fmt.Printf("[%s] Error setting volume: %v\n", time.Now().Format("15:04:05"), err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			isReducedVolume = true
			lastLoudTimestamp = time.Now()
			fmt.Printf("[%s] LOUD AUDIO DETECTED (avg: %.2f)! Reducing volume from %.0f%% to %.0f%%\n",
				time.Now().Format("15:04:05"), averagePeak, originalVolume*100, newVolume*100)
		} else if averagePeak > float32(config.Threshold) && isReducedVolume {
			lastLoudTimestamp = time.Now()
		} else if isReducedVolume && time.Since(lastLoudTimestamp) > time.Duration(config.RestoreDelay)*time.Second {
			if err := audioEndpointVolume.SetMasterVolumeLevelScalar(originalVolume, nil); err != nil {
				fmt.Printf("[%s] Error restoring volume: %v\n", time.Now().Format("15:04:05"), err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			isReducedVolume = false
			fmt.Printf("[%s] Audio normal for %ds. Restoring volume to %.0f%%\n",
				time.Now().Format("15:04:05"), config.RestoreDelay, originalVolume*100)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

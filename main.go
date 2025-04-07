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
}

var defaultConfig = Config{
	Threshold:      0.4,
	DivisionFactor: 4.0,
	RestoreDelay:   3,
	Verbose:        false,
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
		fmt.Printf("Created default configuration file at: %s\n", filePath)
		return config, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return config, fmt.Errorf("failed to read config file: %v", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("failed to parse config file: %v", err)
	}

	fmt.Printf("Loaded configuration from: %s\n", filePath)
	return config, nil
}

func main() {
	flag.Parse()

	config, err := loadConfig(*configFile)
	if err != nil {
		fmt.Printf("Warning: %v\n", err)
		fmt.Println("Using default configuration")
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

	fmt.Println("Starting EarGuard...")
	fmt.Printf("Settings: Threshold: %.2f, Volume division: 1/%.0f, Restore delay: %ds, Verbose: %v\n",
		config.Threshold, config.DivisionFactor, config.RestoreDelay, config.Verbose)

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

	fmt.Println("Audio monitoring active - Press Ctrl+C to exit")

	var isReducedVolume bool = false
	var originalVolume float32
	var lastLoudTimestamp time.Time
	var lastPrintTime time.Time = time.Now()

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		fmt.Println("Shutting down...")
		if isReducedVolume {
			fmt.Println("Restoring volume...")
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

		var currentVolume float32
		if err := audioEndpointVolume.GetMasterVolumeLevelScalar(&currentVolume); err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if config.Verbose && time.Since(lastPrintTime) > 2*time.Second {
			fmt.Printf("Current peak: %.2f, Volume: %.0f%%, Reduced: %v\n",
				peakValue, currentVolume*100, isReducedVolume)
			lastPrintTime = time.Now()
		}

		if peakValue > float32(config.Threshold) && !isReducedVolume {
			originalVolume = currentVolume
			newVolume := originalVolume / float32(config.DivisionFactor)
			if err := audioEndpointVolume.SetMasterVolumeLevelScalar(newVolume, nil); err != nil {
				fmt.Printf("Error setting volume: %v\n", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			isReducedVolume = true
			lastLoudTimestamp = time.Now()
			fmt.Printf("LOUD AUDIO DETECTED (%.2f)! Reducing volume from %.0f%% to %.0f%%\n",
				peakValue, originalVolume*100, newVolume*100)
		} else if peakValue > float32(config.Threshold) && isReducedVolume {
			lastLoudTimestamp = time.Now()
		} else if isReducedVolume && time.Since(lastLoudTimestamp) > time.Duration(config.RestoreDelay)*time.Second {
			if err := audioEndpointVolume.SetMasterVolumeLevelScalar(originalVolume, nil); err != nil {
				fmt.Printf("Error restoring volume: %v\n", err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			isReducedVolume = false
			fmt.Printf("Audio normal for %ds. Restoring volume to %.0f%%\n",
				config.RestoreDelay, originalVolume*100)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

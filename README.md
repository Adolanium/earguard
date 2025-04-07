# EarGuard

A simple tool to automatically reduce volume when loud audio is detected to protect your ears from sudden loud sounds.

## Features

- Monitors audio output and detects sudden loud sounds
- Automatically reduces volume when audio peak exceeds threshold
- Restores original volume after sound returns to normal levels
- Configurable via config file or command-line arguments

## Usage

```
earguard.exe [options]
```

### Command Line Options

- `-threshold float`: Audio peak threshold (0.0-1.0)
- `-division float`: Factor to divide volume by when loud sound detected
- `-delay int`: Seconds to wait before restoring volume
- `-verbose`: Enable verbose output
- `-config string`: Path to configuration file

## Configuration

The application can be configured using a JSON configuration file. By default, the application looks for a `config.json` file in the same directory as the executable. You can specify a custom configuration file path using the `-config` option.

Example configuration file:

```json
{
  "threshold": 0.4,
  "division_factor": 4.0,
  "restore_delay": 3,
  "verbose": false
}
```

### Configuration Options

- `threshold`: Audio peak threshold (0.0-1.0). When the audio peak exceeds this value, volume will be reduced. Default: 0.4
- `division_factor`: Factor to divide volume by when loud sound is detected. Higher values mean more reduction. Default: 4.0
- `restore_delay`: Seconds to wait before restoring volume to normal after loud sounds stop. Default: 3
- `verbose`: Whether to print detailed audio levels during operation. Default: false

## Notes

- Command-line options override configuration file settings
- If no configuration file is found, a default one will be created 
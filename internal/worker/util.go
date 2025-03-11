package worker

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// GetAvailableCompilers finds installed compilers on the system
func GetAvailableCompilers() []string {
	compilers := []string{}

	// List of common compilers to check
	possibleCompilers := []string{"gcc", "g++", "clang", "clang++"}

	for _, compiler := range possibleCompilers {
		// Check if compiler exists in PATH
		path, err := exec.LookPath(compiler)
		if err == nil {
			log.Printf("Found compiler: %s at %s", compiler, path)
			compilers = append(compilers, compiler)
		}
	}

	// If no compilers found, use default list
	if len(compilers) == 0 {
		log.Printf("No compilers detected, using default list")
		return possibleCompilers
	}

	return compilers
}

// GetAvailableLinkers finds installed linkers on the system
func GetAvailableLinkers() []string {
	linkers := []string{}

	// List of common linkers to check
	possibleLinkers := []string{"ld", "gold", "lld"}

	for _, linker := range possibleLinkers {
		// Check if linker exists in PATH
		path, err := exec.LookPath(linker)
		if err == nil {
			log.Printf("Found linker: %s at %s", linker, path)
			linkers = append(linkers, linker)
		}
	}

	// If no linkers found, use default list
	if len(linkers) == 0 {
		log.Printf("No linkers detected, using default list")
		return possibleLinkers
	}

	return linkers
}

// GetAvailableMemory reads system memory information on Linux
func GetAvailableMemory() int32 {
	// Default if Getion fails
	defaultMemoryMB := int32(4096)

	// Read /proc/meminfo
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		log.Printf("Failed to read memory info: %v, using default of %dMB", err, defaultMemoryMB)
		return defaultMemoryMB
	}

	// Parse for MemTotal line
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			// Extract the memory value
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// Value is in KB, convert to MB
				memKB, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					log.Printf("Failed to parse memory value: %v, using default", err)
					return defaultMemoryMB
				}

				// Convert to MB and leave some headroom (80% of total)
				memoryMB := int32((memKB / 1024) * 80 / 100)
				log.Printf("Geted %d MB of available memory", memoryMB)
				return memoryMB
			}
		}
	}

	log.Printf("Could not find memory information, using default of %dMB", defaultMemoryMB)
	return defaultMemoryMB
}

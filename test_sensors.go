package main

import (
	"context"
	"fmt"
	"log"

	"github.com/shirou/gopsutil/v4/sensors"
)

func main() {
	fmt.Println("Testing temperature sensors...")

	temperatures, err := sensors.SensorsTemperatures()
	if err != nil {
		log.Printf("Error getting temperatures: %v", err)
		return
	}

	fmt.Printf("Found %d temperature sensors:\n", len(temperatures))
	for i, temp := range temperatures {
		fmt.Printf("[%d] Key: %s, Temperature: %.2f°C\n", i, temp.SensorKey, temp.Temperature)
	}

	// Also try with context
	fmt.Println("\nTrying with context...")
	ctx := context.Background()
	temperatures2, err := sensors.TemperaturesWithContext(ctx)
	if err != nil {
		log.Printf("Error getting temperatures with context: %v", err)
		return
	}

	fmt.Printf("Found %d temperature sensors with context:\n", len(temperatures2))
	for i, temp := range temperatures2 {
		fmt.Printf("[%d] Key: %s, Temperature: %.2f°C\n", i, temp.SensorKey, temp.Temperature)
	}
}

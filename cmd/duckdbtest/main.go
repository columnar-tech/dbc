package main

import (
	"context"
	"fmt"

	"github.com/apache/arrow-adbc/go/adbc/drivermgr"
)

// func installDuckDBWithDbc(ctx context.Context) error {
// 	wd, err := os.Getwd()
// 	if err != nil {
// 		return fmt.Errorf("failed to get working directory: %v", err)
// 	}
// 	installCmd := exec.CommandContext(ctx, filepath.Join(wd, "dbc"), "install", "duckdb")
// 	output, err := installCmd.CombinedOutput()
// 	if err != nil {
// 		return fmt.Errorf("failed to install duckdb driver: %v, output: %s", err, string(output))
// 	}
// 	fmt.Println(string(output))
// 	return nil
// }

func main() {
	ctx := context.Background()
	// if err := installDuckDBWithDbc(ctx); err != nil {
	// 	fmt.Println("Error:", err)
	// }

	var drv drivermgr.Driver
	db, err := drv.NewDatabase(map[string]string{
		"driver": "duckdb",
		"path":   ":memory:",
	})
	if err != nil {
		fmt.Println("Error:", err)
	}
	defer db.Close()

	conn, err := db.Open(ctx)
	if err != nil {
		fmt.Println("Error:", err)
	}
	defer conn.Close()
}

package cmd

import (
	"archive/internal/archiver"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var outZip string

var zipCmd = &cobra.Command{
	Use:   "zip <src>",
	Short: "Запаковать файл/папку в ZIP (параллельно) путь , -o созданием с определенным именим",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]

		// Проверка существования пути
		if _, err := os.Stat(src); os.IsNotExist(err) {
			return fmt.Errorf("указанный путь не существует: %s", src)
		}

		// Если имя архива не указано — берём имя папки/файла + .zip
		if outZip == "" {
			base := filepath.Base(src)
			outZip = base + ".zip"
		}

		// Запускаем упаковку
		if err := archiver.ZipParallelAll(src, outZip); err != nil {
			return fmt.Errorf("ошибка при упаковке: %w", err)
		}

		fmt.Println("✅ Архив создан:", outZip)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(zipCmd)
	zipCmd.Flags().StringVarP(&outZip, "output", "o", "", "путь к выходному .zip")
}

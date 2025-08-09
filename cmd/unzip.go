package cmd

import (
	"archive/internal/archiver"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var destDir string

var unzipCmd = &cobra.Command{
	Use:   "unzip <zipfile>",
	Short: "-d = Распаковать ZIP в директорию (параллельно) , -d созданием с определенным именим",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		zipfile := args[0]

		// Проверка, что архив существует
		if _, err := os.Stat(zipfile); os.IsNotExist(err) {
			return fmt.Errorf("архив не найден: %s", zipfile)
		}

		// Если папка не указана — берём имя архива без расширений
		if destDir == "" {
			base := filepath.Base(zipfile)
			if strings.EqualFold(filepath.Ext(base), ".zip") {
				base = strings.TrimSuffix(base, filepath.Ext(base))
			}
			if ext2 := filepath.Ext(base); ext2 != "" {
				base = strings.TrimSuffix(base, ext2)
			}
			destDir = base
		}

		// Запускаем распаковку
		if err := archiver.UnzipParallelAll(zipfile, destDir); err != nil {
			return fmt.Errorf("ошибка при распаковке: %w", err)
		}

		fmt.Println("✅ Распаковано в:", destDir)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(unzipCmd)
	unzipCmd.Flags().StringVarP(&destDir, "dest", "d", "", "директория распаковки")
}

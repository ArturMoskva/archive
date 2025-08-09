package archiver

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ZipParallelAll упаковывает src в zipPath, используя все доступные CPU для параллельной подготовки/чтения.
// Запись в zip.Writer выполняется последовательно (как требует формат ZIP).
func ZipParallelAll(src, zipPath string) error {
	src = filepath.Clean(src)

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}

	// Сбор путей (детерминированный порядок для стабильных архивов).
	paths := make([]string, 0, 1024)
	if err := filepath.WalkDir(src, func(p string, d fs.DirEntry, we error) error {
		if we != nil {
			return we
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)

	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	// baseDir — имя корня внутри архива для директории.
	baseDir := ""
	if info.IsDir() {
		baseDir = filepath.Base(src)
	}

	type result struct {
		idx   int
		hdr   *zip.FileHeader
		write func(w io.Writer) error // nil для каталогов (пустые записи)
		err   error
	}

	// Вспомогательные функции (замыкания), чтобы держать всё в одной функции:
	indexOf := func(arr []string, x string) int {
		for i, v := range arr {
			if v == x {
				return i
			}
		}
		return -1
	}
	toZipPath := func(p string) string {
		return strings.ReplaceAll(p, string(os.PathSeparator), "/")
	}
	relInside := func(srcRoot, base, full string) string {
		if base == "" { // один файл
			return filepath.Base(full)
		}
		if full == srcRoot {
			return base
		}
		rel, _ := filepath.Rel(filepath.Dir(srcRoot), full)
		return rel
	}
	writeOne := func(r result) error {
		if r.hdr == nil && r.write == nil {
			return nil // корневой dir, ничего не пишем
		}
		if r.hdr != nil && r.write == nil {
			_, err := zw.CreateHeader(r.hdr)
			return err
		}
		w, err := zw.CreateHeader(r.hdr)
		if err != nil {
			return err
		}
		return r.write(w)
	}

	jobs := runtime.NumCPU()
	jobsCh := make(chan string, 2*jobs)
	resCh := make(chan result, 2*jobs)

	// Воркеры: готовят заголовки и функцию записи.
	var wg sync.WaitGroup
	wg.Add(jobs)
	for i := 0; i < jobs; i++ {
		go func() {
			defer wg.Done()
			for p := range jobsCh {
				idx := indexOf(paths, p)
				rel := relInside(src, baseDir, p)

				st, e := os.Stat(p)
				if e != nil {
					resCh <- result{idx: idx, err: e}
					continue
				}
				if st.IsDir() {
					if rel != "" {
						h := &zip.FileHeader{
							Name:     toZipPath(rel) + "/",
							Method:   zip.Store,
							Modified: time.Now(), // можно st.ModTime()
						}
						resCh <- result{idx: idx, hdr: h}
					} else {
						resCh <- result{idx: idx} // корень директории
					}
					continue
				}

				hdr, e := zip.FileInfoHeader(st)
				if e != nil {
					resCh <- result{idx: idx, err: e}
					continue
				}
				hdr.Name = toZipPath(rel)
				hdr.Method = zip.Deflate
				hdr.Modified = time.Now()     // можно st.ModTime()
				hdr.SetMode(st.Mode().Perm()) // сохранить права

				writeFn := func(w io.Writer) error {
					f, openErr := os.Open(p)
					if openErr != nil {
						return openErr
					}
					defer f.Close()
					_, cpErr := io.Copy(w, f)
					return cpErr
				}

				resCh <- result{idx: idx, hdr: hdr, write: writeFn}
			}
		}()
	}

	// Подаём задания.
	go func() {
		for _, p := range paths {
			jobsCh <- p
		}
		close(jobsCh)
	}()

	// Закрываем resCh после завершения воркеров.
	go func() {
		wg.Wait()
		close(resCh)
	}()

	// Последовательная запись по исходному порядку.
	next := 0
	pending := make(map[int]result, 128)

	for {
		if r, ok := pending[next]; ok {
			if r.err != nil {
				return r.err
			}
			if err := writeOne(r); err != nil {
				return err
			}
			delete(pending, next)
			next++
			continue
		}
		r, ok := <-resCh
		if !ok {
			// Канал закрыт — дожимаем оставшиеся по порядку.
			for {
				r2, ok2 := pending[next]
				if !ok2 {
					break
				}
				if r2.err != nil {
					return r2.err
				}
				if err := writeOne(r2); err != nil {
					return err
				}
				delete(pending, next)
				next++
			}
			break
		}
		if r.idx == next {
			if r.err != nil {
				return r.err
			}
			if err := writeOne(r); err != nil {
				return err
			}
			next++
			for {
				r2, ok2 := pending[next]
				if !ok2 {
					break
				}
				if r2.err != nil {
					return r2.err
				}
				if err := writeOne(r2); err != nil {
					return err
				}
				delete(pending, next)
				next++
			}
		} else {
			pending[r.idx] = r
		}
	}

	// Финализация файла.
	if err := zw.Close(); err != nil {
		return err
	}
	return out.Close()
}

func UnzipParallelAll(zipPath, destDir string) error {
	// Открываем архив
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Создаём целевую директорию
	if err := os.MkdirAll(destDir, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}

	// Сначала быстро создадим все каталоги (это дешёво и линейно)
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			target := filepath.Join(destDir, f.Name)
			// zip-slip защита
			if err := ensureInside(destDir, target); err != nil {
				return err
			}
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil && !errors.Is(mkErr, fs.ErrExist) {
				return mkErr
			}
		}
	}

	// Параллельно распакуем файлы
	workers := runtime.NumCPU()
	sem := make(chan struct{}, workers) // семафор параллельности
	var wg sync.WaitGroup

	// Канал для первой же ошибки (если случится)
	errOnce := make(chan error, 1)
	setErr := func(e error) {
		select { case errOnce <- e: default: }
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}

		wg.Add(1)
		go func(f *zip.File) {
			defer wg.Done()

			// Лимитируем число одновременных работ
			sem <- struct{}{}
			defer func() { <-sem }()

			target := filepath.Join(destDir, f.Name)

			// zip-slip защита
			if err := ensureInside(destDir, target); err != nil {
				setErr(err)
				return
			}

			// Создадим родительский каталог, если не успели выше
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				setErr(err)
				return
			}

			rc, err := f.Open()
			if err != nil {
				setErr(err)
				return
			}
			defer rc.Close()

			// Создаём файл с исходными правами
			dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
			if err != nil {
				setErr(err)
				return
			}

			// Копируем содержимое
			if _, err := io.Copy(dst, rc); err != nil {
				dst.Close()
				setErr(err)
				return
			}
			if err := dst.Close(); err != nil {
				setErr(err)
				return
			}

			// Восстановим mtime (atime ставим текущее)
			_ = os.Chtimes(target, time.Now(), f.Modified)
		}(f)
	}

	// Ждём завершения всех горутин
	wg.Wait()

	// Возвращаем первую ошибку, если была
	select {
	case e := <-errOnce:
		return e
	default:
		return nil
	}
}

// ensureInside проверяет, что target находится внутри baseDir (защита от zip-slip).
func ensureInside(baseDir, target string) error {
	cleanBase := filepath.Clean(baseDir)
	cleanTarget := filepath.Clean(target)

	// Нормализуем разделители для сравнения префикса
	if cleanTarget == cleanBase {
		return nil
	}
	withSep := cleanBase + string(os.PathSeparator)
	if !strings.HasPrefix(cleanTarget, withSep) {
		return errors.New("zip slip detected: " + target)
	}
	return nil
}
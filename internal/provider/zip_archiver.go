// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package archive

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

type ZipArchiver struct {
	filepath       string
	outputFileMode string // Default value "" means unset
	filewriter     *os.File
	zipwriter      *zip.Writer
}

func NewZipArchiver(filepath string) Archiver {
	return &ZipArchiver{
		filepath: filepath,
	}
}

func (a *ZipArchiver) ArchiveContent(content []byte, infilename string) error {
	if err := a.open(); err != nil {
		return err
	}
	defer a.close()

	f, err := a.zipwriter.Create(filepath.ToSlash(infilename))
	if err != nil {
		return err
	}

	_, err = f.Write(content)
	return err
}

func (a *ZipArchiver) ArchiveFile(infilename string) error {
	fi, err := assertValidFile(infilename)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(infilename)
	if err != nil {
		return err
	}

	if err := a.open(); err != nil {
		return err
	}
	defer a.close()

	fh, err := zip.FileInfoHeader(fi)
	if err != nil {
		return fmt.Errorf("error creating file header: %s", err)
	}
	fh.Name = filepath.ToSlash(fi.Name())
	fh.Method = zip.Deflate
	//nolint:staticcheck // This is required as fh.SetModTime has been deprecated since Go 1.10 and using fh.Modified alone isn't enough when using a zero value
	fh.SetModTime(time.Time{})

	if a.outputFileMode != "" {
		filemode, err := strconv.ParseUint(a.outputFileMode, 0, 32)
		if err != nil {
			return fmt.Errorf("error parsing output_file_mode value: %s", a.outputFileMode)
		}
		fh.SetMode(os.FileMode(filemode))
	}

	f, err := a.zipwriter.CreateHeader(fh)
	if err != nil {
		return fmt.Errorf("error creating file inside archive: %s", err)
	}

	_, err = f.Write(content)
	return err
}

func (a *ZipArchiver) ArchiveUrl(inurlname string) error {
	return nil
}

func (a *ZipArchiver) ArchiveDir(indirname string, opts ArchiveDirOpts) error {
	if err := assertValidDir(indirname); err != nil {
		return err
	}

	// ensure exclusions are OS compatible paths
	for i := range opts.Excludes {
		opts.Excludes[i] = filepath.FromSlash(opts.Excludes[i])
	}

	if err := a.open(); err != nil {
		return err
	}
	defer a.close()

	return filepath.Walk(indirname, a.createWalkFunc("", indirname, opts))
}

func (a *ZipArchiver) createWalkFunc(basePath string, indirname string, opts ArchiveDirOpts) func(path string, info os.FileInfo, err error) error {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error encountered during file walk: %s", err)
		}

		relName, err := filepath.Rel(indirname, path)
		if err != nil {
			return fmt.Errorf("error relativizing file for archival: %s", err)
		}

		archivePath := filepath.Join(basePath, relName)

		isExcluded, err := checkMatch(archivePath, opts.Excludes)
		if err != nil {
			return fmt.Errorf("error matching file for archival: %s", err)
		}

		if info.IsDir() {
			if isExcluded {
				return filepath.SkipDir
			}

			if archivePath != "." {
				_, err := a.zipwriter.Create(archivePath + "/")
				if err != nil {
					return fmt.Errorf("error adding directory for archival: %s", err)
				}
			}

			return nil
		}

		if isExcluded {
			return nil
		}

		if info.Mode()&os.ModeSymlink == os.ModeSymlink {
			if !opts.ExcludeSymlinkDirectories {
				realPath, err := filepath.EvalSymlinks(path)
				if err != nil {
					return err
				}

				realInfo, err := os.Stat(realPath)
				if err != nil {
					return err
				}

				if realInfo.IsDir() {
					return filepath.Walk(realPath, a.createWalkFunc(archivePath, realPath, opts))
				}

				info = realInfo
			}
		}

		fh, err := zip.FileInfoHeader(info)
		if err != nil {
			return fmt.Errorf("error creating file header: %s", err)
		}
		fh.Name = filepath.ToSlash(archivePath)
		fh.Method = zip.Deflate
		// fh.Modified alone isn't enough when using a zero value
		//nolint:staticcheck
		fh.SetModTime(time.Time{})

		if a.outputFileMode != "" {
			filemode, err := strconv.ParseUint(a.outputFileMode, 0, 32)
			if err != nil {
				return fmt.Errorf("error parsing output_file_mode value: %s", a.outputFileMode)
			}
			fh.SetMode(os.FileMode(filemode))
		}

		f, err := a.zipwriter.CreateHeader(fh)
		if err != nil {
			return fmt.Errorf("error creating file inside archive: %s", err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("error reading file for archival: %s", err)
		}
		_, err = f.Write(content)
		return err
	}
}

func (a *ZipArchiver) ArchiveMultiple(content map[string][]byte) error {
	if err := a.open(); err != nil {
		return err
	}
	defer a.close()

	// Ensure files are processed in the same order so hashes don't change
	keys := make([]string, len(content))
	i := 0
	for k := range content {
		keys[i] = k
		i++
	}
	sort.Strings(keys)

	for _, filename := range keys {
		f, err := a.zipwriter.Create(filepath.ToSlash(filename))
		if err != nil {
			return err
		}
		_, err = f.Write(content[filename])
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *ZipArchiver) SetOutputFileMode(outputFileMode string) {
	a.outputFileMode = outputFileMode
}

func (a *ZipArchiver) open() error {
	f, err := os.Create(a.filepath)
	if err != nil {
		return err
	}
	a.filewriter = f
	a.zipwriter = zip.NewWriter(f)
	return nil
}

func (a *ZipArchiver) close() {
	if a.zipwriter != nil {
		a.zipwriter.Close()
		a.zipwriter = nil
	}
	if a.filewriter != nil {
		a.filewriter.Close()
		a.filewriter = nil
	}
}

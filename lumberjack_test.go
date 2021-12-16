package lumberjack

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const sleepTime = 100 * time.Millisecond

// !!!NOTE!!!
//
// Running these tests in parallel will almost certainly cause sporadic (or even
// regular) failures, because they're all messing with the same global variable
// that controls the logic's mocked time.Now.  So... don't do that.
//
// Run tests sequentially using:
//
//     go test -p 1
//

// Since all the tests uses the time to determine filenames etc, we need to
// control the wall clock as much as possible, which means having a wall clock
// that doesn't change unless we want it to.
var fakeCurrentTime = time.Now().UTC()

func fakeTime() time.Time {
	return fakeCurrentTime
}

func TestNewFile(t *testing.T) {
	currentTime = fakeTime

	dir := makeTempDir("TestNewFile", t)
	defer os.RemoveAll(dir)
	l := &Logger{
		Filename:       logFile(dir),
		MaxLogSizeMB:   100,
		MaxTotalSizeMB: 150,
	}
	defer l.Close()
	b := []byte("boo!")
	n, err := l.Write(b)
	isNil(err, t)
	equals(len(b), n, t)
	existsWithContent(logFile(dir), b, t)
	fileCount(dir, 1, t)

	// Allow time for any underway mill() processing to complete
	<-time.After(sleepTime)
}

func TestOpenExisting(t *testing.T) {
	currentTime = fakeTime
	dir := makeTempDir("TestOpenExisting", t)
	defer os.RemoveAll(dir)

	filename := logFile(dir)
	data := []byte("foo!")
	err := ioutil.WriteFile(filename, data, fileMode)
	isNil(err, t)
	existsWithContent(filename, data, t)

	l := &Logger{
		Filename:       filename,
		MaxLogSizeMB:   100,
		MaxTotalSizeMB: 150,
	}
	defer l.Close()
	b := []byte("boo!")
	n, err := l.Write(b)
	isNil(err, t)
	equals(len(b), n, t)

	// make sure the file got appended
	existsWithContent(filename, append(data, b...), t)

	// make sure no other files were created
	fileCount(dir, 1, t)

	<-time.After(sleepTime)
}

func TestFirstWriteRotate(t *testing.T) {
	currentTime = fakeTime
	MB = 1
	dir := makeTempDir("TestFirstWriteRotate", t)
	defer os.RemoveAll(dir)

	filename := logFile(dir)
	l := &Logger{
		Filename:       filename,
		MaxLogSizeMB:   6,
		MaxTotalSizeMB: 50,
	}
	defer l.Close()

	start := []byte("data")
	err := ioutil.WriteFile(filename, start, fileMode)
	isNil(err, t)
	existsWithContent(filename, start, t)

	newFakeTime()

	// this would make us rotate
	b := []byte("foooooo!")
	n, err := l.Write(b)
	isNil(err, t)
	equals(len(b), n, t)

	<-time.After(sleepTime)

	existsWithContent(filename, b, t)

	bc := new(bytes.Buffer)
	gz := gzip.NewWriter(bc)
	_, err = gz.Write(start)
	isNil(err, t)
	err = gz.Close()
	isNil(err, t)
	existsWithContent(backupFile(dir)+compressSuffix, bc.Bytes(), t)

	fileCount(dir, 2, t)

	<-time.After(sleepTime)
}

func TestCleanupExistingBackups(t *testing.T) {
	// test that if we start with more backup files than we're supposed to have
	// in total, that extra ones get cleaned up when we rotate.

	currentTime = fakeTime
	MB = 1

	dir := makeTempDir("TestCleanupExistingBackups", t)
	defer os.RemoveAll(dir)

	// make 3 backup files

	data := []byte("data")
	backup := backupFile(dir)
	err := ioutil.WriteFile(backup+compressSuffix, data, fileMode)
	isNil(err, t)

	newFakeTime()

	backup = backupFile(dir)
	err = ioutil.WriteFile(backup+compressSuffix, data, fileMode)
	isNil(err, t)

	newFakeTime()

	backup = backupFile(dir)
	err = ioutil.WriteFile(backup+compressSuffix, data, fileMode)
	isNil(err, t)

	// now create a primary log file with some data
	filename := logFile(dir)
	err = ioutil.WriteFile(filename, data, fileMode)
	isNil(err, t)
	l := &Logger{
		Filename:       filename,
		MaxLogSizeMB:   10,
		MaxTotalSizeMB: 40, /* The first rotation will create a 28-byte gzipped file */
	}
	defer l.Close()

	newFakeTime()

	b2 := []byte("foooooo!")
	n, err := l.Write(b2)
	isNil(err, t)
	equals(len(b2), n, t)

	<-time.After(sleepTime)

	// now we should only have 2 files left - the primary and one backup
	fileCount(dir, 2, t)

	<-time.After(sleepTime)
}

func TestOldLogFiles(t *testing.T) {
	currentTime = fakeTime
	MB = 1

	dir := makeTempDir("TestOldLogFiles", t)
	defer os.RemoveAll(dir)

	filename := logFile(dir)
	data := []byte("data")
	err := ioutil.WriteFile(filename, data, 07)
	isNil(err, t)

	// This gives us a time with the same precision as the time we get from the
	// timestamp in the name.
	t1 := time.Unix(fakeTime().Unix(), 0).UTC()

	backup := backupFile(dir)
	err = ioutil.WriteFile(backup, data, 07)
	isNil(err, t)

	newFakeTime()

	t2 := time.Unix(fakeTime().Unix(), 0).UTC()

	backup2 := backupFile(dir)
	err = ioutil.WriteFile(backup2, data, 07)
	isNil(err, t)

	l := &Logger{Filename: filename}
	files, err := l.oldLogFiles()
	isNil(err, t)
	equals(2, len(files), t)

	// should be sorted by newest file first, which would be t2
	equals(t2, files[0].timestamp, t)
	equals(t1, files[1].timestamp, t)

	<-time.After(sleepTime)
}

func TestTimeFromName(t *testing.T) {
	l := &Logger{Filename: "/var/log/myfoo/foo.log"}
	prefix, ext := l.prefixAndExt()

	tests := []struct {
		filename string
		want     time.Time
		wantErr  bool
	}{
		{"foo-1399214673.log", time.Date(2014, 5, 4, 14, 44, 33, 000000000, time.UTC), false},
		{"foo-1399214673", time.Time{}, true},
		{"1399214673.log", time.Time{}, true},
		{"foo.log", time.Time{}, true},
	}

	for _, test := range tests {
		got, err := l.timeFromName(test.filename, prefix, ext)
		equals(got, test.want, t)
		equals(err != nil, test.wantErr, t)
	}

	<-time.After(sleepTime)
}

func TestRotate(t *testing.T) {
	MB = 1

	currentTime = fakeTime
	dir := makeTempDir("TestRotate", t)
	defer os.RemoveAll(dir)

	filename := logFile(dir)

	l := &Logger{
		Filename:       filename,
		MaxLogSizeMB:   12,
		MaxTotalSizeMB: 77, /* gz files are between 23 and 29 bytes */
	}
	defer l.Close()
	b := []byte("data")
	n, err := l.Write(b)
	isNil(err, t)
	equals(len(b), n, t)

	existsWithContent(filename, b, t)
	fileCount(dir, 1, t)

	newFakeTime()

	err = l.rotate()
	isNil(err, t)

	<-time.After(sleepTime)

	filename2 := backupFile(dir)

	bc := new(bytes.Buffer)
	gz := gzip.NewWriter(bc)
	_, err = gz.Write(b)
	isNil(err, t)
	err = gz.Close()
	isNil(err, t)
	existsWithContent(filename2+compressSuffix, bc.Bytes(), t)

	existsWithContent(filename, []byte{}, t)
	fileCount(dir, 2, t)
	newFakeTime()

	err = l.rotate()
	isNil(err, t)

	<-time.After(sleepTime)

	filename3 := backupFile(dir)

	bc = new(bytes.Buffer)
	gz = gzip.NewWriter(bc)
	_, err = gz.Write([]byte(""))
	isNil(err, t)
	err = gz.Close()
	isNil(err, t)
	existsWithContent(filename3+compressSuffix, bc.Bytes(), t)

	existsWithContent(filename, []byte{}, t)
	fileCount(dir, 3, t)
	newFakeTime()

	b2 := []byte("foooooo!") /* This does not trigger a rotate */
	n, err = l.Write(b2)
	isNil(err, t)
	equals(len(b2), n, t)

	<-time.After(sleepTime)

	fileCount(dir, 3, t)
	newFakeTime()

	b3 := []byte("foooooo!") /* This triggers a rotate */
	n, err = l.Write(b3)
	isNil(err, t)
	equals(len(b3), n, t)

	<-time.After(sleepTime)

	fileCount(dir, 3, t)

	// this will use the new fake time
	existsWithContent(filename, b3, t)

	<-time.After(sleepTime)
}

func TestCompressOnRotate(t *testing.T) {
	currentTime = fakeTime
	MB = 1

	dir := makeTempDir("TestCompressOnRotate", t)
	defer os.RemoveAll(dir)

	filename := logFile(dir)
	l := &Logger{
		Filename:       filename,
		MaxLogSizeMB:   10,
		MaxTotalSizeMB: 50,
	}
	defer l.Close()
	b := []byte("boo!")
	n, err := l.Write(b)
	isNil(err, t)
	equals(len(b), n, t)

	existsWithContent(filename, b, t)
	fileCount(dir, 1, t)

	newFakeTime()

	err = l.rotate()
	isNil(err, t)

	<-time.After(sleepTime)

	// the old logfile should be moved aside and the main logfile should have
	// nothing in it.
	existsWithContent(filename, []byte{}, t)

	// a compressed version of the log file should now exist and the original
	// should have been removed.
	bc := new(bytes.Buffer)
	gz := gzip.NewWriter(bc)
	_, err = gz.Write(b)
	isNil(err, t)
	err = gz.Close()
	isNil(err, t)

	existsWithContent(backupFile(dir)+compressSuffix, bc.Bytes(), t)
	notExist(backupFile(dir), t)

	fileCount(dir, 2, t)

	<-time.After(sleepTime)
}

func TestCompressOnResume(t *testing.T) {
	currentTime = fakeTime
	MB = 1

	dir := makeTempDir("TestCompressOnResume", t)
	defer os.RemoveAll(dir)

	filename := logFile(dir)
	l := &Logger{
		Filename:       filename,
		MaxLogSizeMB:   6,
		MaxTotalSizeMB: 40, /* The first rotation will create a 28-byte gzipped file */
	}
	defer l.Close()

	// Create a backup file and empty "compressed" file.
	filename2 := backupFile(dir)
	b := []byte("foo!")
	err := ioutil.WriteFile(filename2, b, fileMode)
	isNil(err, t)
	err = ioutil.WriteFile(filename2+compressSuffix, []byte{}, fileMode)
	isNil(err, t)

	newFakeTime()
	b2 := []byte("boo!")
	n, err := l.Write(b2)
	isNil(err, t)
	equals(len(b2), n, t)
	existsWithContent(filename, b2, t)

	<-time.After(sleepTime)

	// The write should have started the compression - a compressed version of
	// the log file should now exist and the original should have been removed.
	bc := new(bytes.Buffer)
	gz := gzip.NewWriter(bc)
	_, err = gz.Write(b)
	isNil(err, t)
	err = gz.Close()
	isNil(err, t)
	existsWithContent(filename2+compressSuffix, bc.Bytes(), t)
	notExist(filename2, t)

	fileCount(dir, 2, t)

	<-time.After(sleepTime)
}

// makeTempDir creates a file with a semi-unique name in the OS temp directory.
// It should be based on the name of the test, to keep parallel tests from
// colliding, and must be cleaned up after the test is finished.
func makeTempDir(name string, t testing.TB) string {
	dir := fmt.Sprintf("%s-%d", name, time.Now().UTC().UnixNano())
	dir = filepath.Join(os.TempDir(), dir)
	isNilUp(os.Mkdir(dir, 0700), t, 1)
	return dir
}

// existsWithContent checks that the given file exists and has the correct content.
func existsWithContent(path string, content []byte, t testing.TB) {
	info, err := os.Stat(path)
	isNilUp(err, t, 1)
	equalsUp(int64(len(content)), info.Size(), t, 1)

	b, err := ioutil.ReadFile(path)
	isNilUp(err, t, 1)
	equalsUp(content, b, t, 1)
}

// logFile returns the log file name in the given directory for the current fake
// time.
func logFile(dir string) string {
	return filepath.Join(dir, "foobar.log")
}

func backupFile(dir string) string {
	fname := fmt.Sprintf("foobar-%d.log", fakeTime().Unix())
	return filepath.Join(dir, fname)
}

// fileCount checks that the number of files in the directory is exp.
func fileCount(dir string, exp int, t testing.TB) {
	files, err := ioutil.ReadDir(dir)
	isNilUp(err, t, 1)
	// Make sure no other files were created.
	equalsUp(exp, len(files), t, 1)
}

// newFakeTime sets the fake "current time" to two days later.
func newFakeTime() {
	fakeCurrentTime = fakeCurrentTime.Add(time.Hour * 24 * 2)
}

func notExist(path string, t testing.TB) {
	_, err := os.Stat(path)
	assertUp(os.IsNotExist(err), t, 1, "expected to get os.IsNotExist, but instead got %v", err)
}

func exists(path string, t testing.TB) {
	_, err := os.Stat(path)
	assertUp(err == nil, t, 1, "expected file to exist, but got error from os.Stat: %v", err)
}

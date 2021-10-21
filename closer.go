// modified from https://github.com/xlab/closer
package closer

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
)

var (
	DebugSignalSet = []os.Signal{
		syscall.SIGINT,
		syscall.SIGHUP,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	}
	DefaultSignalSet = append(DebugSignalSet, syscall.SIGABRT)
)

var (
	// ExitCodeOK is a successfull exit code.
	ExitCodeOK = 0
	// ExitCodeErr is a failure exit code.
	ExitCodeErr = 1
	// ExitSignals is the active list of signals to watch for.
	ExitSignals = DefaultSignalSet
)

// Config should be used with Init function to override the defaults.
type Config struct {
	ExitCodeOK  int
	ExitCodeErr int
	ExitSignals []os.Signal
}

var c = newCloser()

type closer struct {
	codeOK     int
	codeErr    int
	signals    []os.Signal
	sem        sync.Mutex
	closeOnce  sync.Once
	ctrlC      []func()
	ctrlSlash  []func()
	errChan    chan struct{}
	doneChan   chan struct{}
	signalChan chan os.Signal
	closeChan  chan struct{}
	holdChan   chan struct{}
	//
	cancelWaitChan chan struct{}
}

func newCloser() *closer {
	c := &closer{
		codeOK:  ExitCodeOK,
		codeErr: ExitCodeErr,
		signals: ExitSignals,
		//
		errChan:    make(chan struct{}),
		doneChan:   make(chan struct{}),
		signalChan: make(chan os.Signal, 1),
		closeChan:  make(chan struct{}),
		holdChan:   make(chan struct{}),
		//
		cancelWaitChan: make(chan struct{}),
	}

	signal.Notify(c.signalChan, c.signals...)

	// start waiting
	go c.wait()
	return c
}

func (c *closer) wait() {
	var way string
	exitCode := c.codeOK

	// wait for a close request
	select {
	case <-c.cancelWaitChan:
		return
	case sig := <-c.signalChan:
		switch sig {
		case syscall.SIGQUIT: // press ctrl + \
			way = "slash"
		case syscall.SIGINT: // pres ctrl + c
			way = "c"
		}
	case <-c.closeChan:
		break
	case <-c.errChan:
		exitCode = c.codeErr
	}

	// ensure we'll exit
	defer os.Exit(exitCode)

	c.sem.Lock()
	defer c.sem.Unlock()
	switch way {
	case "c":
		for _, fn := range c.ctrlC {
			fn()
		}
	case "slash":
		for _, fn := range c.ctrlSlash {
			fn()
		}
	}

	for _, fn := range c.ctrlSlash {
		fn()
	}
	// done!
	close(c.doneChan)
}

// Close sends a close request.
// The app will be terminated by OS as soon as the first close request will be handled by closer, this
// function will return no sooner. The exit code will always be 0 (success).
func Close() {
	// check if there was a panic
	if x := recover(); x != nil {
		var (
			offset int = 3
			pc     uintptr
			ok     bool
		)
		log.Printf("run time panic: %v", x)
		for offset < 32 {
			pc, _, _, ok = runtime.Caller(offset)
			if !ok {
				// close with an error
				c.closeErr()
				return
			}
			frame := newStackFrame(pc)
			fmt.Print(frame.String())
			offset++
		}
		// close with an error
		c.closeErr()
		return
	}
	// normal close
	c.closeOnce.Do(func() {
		close(c.closeChan)
	})
	<-c.doneChan
}

// Fatalln works the same as log.Fatalln but respects the closer's logic.
func Fatalln(v ...interface{}) {
	out := log.New(os.Stderr, "", log.Flags())
	out.Output(2, fmt.Sprintln(v...))
	c.closeErr()
}

// Fatalf works the same as log.Fatalf but respects the closer's logic.
func Fatalf(format string, v ...interface{}) {
	out := log.New(os.Stderr, "", log.Flags())
	out.Output(2, fmt.Sprintf(format, v...))
	c.closeErr()
}

// Exit is the same as os.Exit but respects the closer's logic. It converts
// any error code into ExitCodeErr (= 1, by default).
func Exit(code int) {
	// check if there was a panic
	if x := recover(); x != nil {
		var (
			offset int = 3
			pc     uintptr
			ok     bool
		)
		log.Printf("run time panic: %v", x)
		for offset < 32 {
			pc, _, _, ok = runtime.Caller(offset)
			if !ok {
				// close with an error
				c.closeErr()
				return
			}
			frame := newStackFrame(pc)
			fmt.Print(frame.String())
			offset++
		}
		// close with an error
		c.closeErr()
		return
	}
	if code == ExitCodeOK {
		c.closeOnce.Do(func() {
			close(c.closeChan)
		})
		<-c.doneChan
		return
	}
	c.closeErr()
}

func (c *closer) closeErr() {
	c.closeOnce.Do(func() {
		close(c.errChan)
	})
	<-c.doneChan
}

func CtrlPlusCBind(cleanup func()) {
	c.sem.Lock()
	// store in the reverse order
	s := make([]func(), 0, 1+len(c.ctrlC))
	s = append(s, cleanup)
	c.ctrlC = append(s, c.ctrlC...)
	c.sem.Unlock()
}

func CtrlPlusSlashBind(cleanup func()) {
	c.sem.Lock()
	// store in the reverse order
	s := make([]func(), 0, 1+len(c.ctrlSlash))
	s = append(s, cleanup)
	c.ctrlSlash = append(s, c.ctrlSlash...)
	c.sem.Unlock()
}

// Checked runs the target function and checks for panics and errors it may yield. In case of panic or error, closer
// will terminate the app with an error code, but either case it will call all the bound callbacks beforehand.
// One can use this instead of `defer` if you need to care about errors and panics that always may happen.
// This function optionally can emit log messages via standard `log` package.
func Checked(target func() error, logging bool) {
	defer func() {
		// check if there was a panic
		if x := recover(); x != nil {
			if logging {
				log.Printf("run time panic: %v", x)
			}
			// close with an error
			c.closeErr()
		}
	}()
	if err := target(); err != nil {
		if logging {
			log.Println("error:", err)
		}
		// close with an error
		c.closeErr()
	}
}

// Hold is a helper that may be used to hold the main from returning,
// until the closer will do a proper exit via `os.Exit`.
func Hold() {
	<-c.holdChan
}

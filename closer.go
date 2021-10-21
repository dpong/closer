package closer

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type closer struct {
	ctrlC     []func()
	ctrlSlash []func()
	sync.Mutex
}

func NewCloser() *closer {
	c := closer{}
	return &c
}

func (c *closer) Hold() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGQUIT, syscall.SIGINT)
	for {
		s := <-ch
		switch s {
		case syscall.SIGQUIT: // press ctrl + \
			c.Lock()
			defer c.Unlock()
			for _, fn := range c.ctrlSlash {
				fn()
			}
			return
		case syscall.SIGINT: // pres ctrl + c
			c.Lock()
			defer c.Unlock()
			for _, fn := range c.ctrlC {
				fn()
			}
			return
		default:
			// pass
		}
	}
	end := make(chan struct{})
	<-end
}

func (c *closer) CtrlPlusCBind(cleanup func()) {
	c.Lock()
	defer c.Unlock()
	s := make([]func(), 0, 1+len(c.ctrlC))
	s = append(s, cleanup)
	c.ctrlC = append(s, c.ctrlC...)
}

func (c *closer) CtrlPlusSlashBind(cleanup func()) {
	c.Lock()
	defer c.Unlock()
	s := make([]func(), 0, 1+len(c.ctrlSlash))
	s = append(s, cleanup)
	c.ctrlSlash = append(s, c.ctrlSlash...)
}

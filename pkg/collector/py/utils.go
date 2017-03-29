package py

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/sbinet/go-python"
)

// #include <Python.h>
import "C"

// StickyLock is a convenient wrapper to interact with the Python GIL
// from go code when using `go-python`.
//
// We are going to call the Python C API from different goroutines that
// in turn will be executed on mulitple, different threads, making the
// Agent incur in this [0] sort of problems.
//
// In addition, the Go runtime might decide to pause a goroutine in a
// thread and resume it later in a different one but we cannot allow this:
// in fact, the Python interpreter will check lock/unlock requests against
// the thread ID they are called from, raising a runtime assertion if
// they don't match. To avoid this, even if giving up on some performance,
// we ask the go runtime to be sure any goroutine using a `StickyLock`
// will be always paused and resumed on the same thread.
//
// [0]: https://docs.python.org/2/c-api/init.html#non-python-created-threads
type StickyLock struct {
	gstate python.PyGILState
}

// NewStickyLock register the current thread with the interpreter and locks
// the GIL. It also sticks the goroutine to the current thread so that a
// subsequent call to `Unlock` will unregister the very same thread.
func NewStickyLock() *StickyLock {
	runtime.LockOSThread()
	return &StickyLock{
		gstate: python.PyGILState_Ensure(),
	}
}

// Unlock deregisters the current thread from the interpreter, unlocks the GIL
// and detaches the goroutine from the current thread.
func (sl *StickyLock) Unlock() {
	python.PyGILState_Release(sl.gstate)
	runtime.UnlockOSThread()
}

// Initialize wraps all the operations needed to start the Python interpreter and
// configure the environment. This function should be called at most once in the
// Agent lifetime.
func Initialize(paths ...string) *python.PyThreadState {
	// Disable Site initialization
	C.Py_NoSiteFlag = 1

	// Start the interpreter
	if C.Py_IsInitialized() == 0 {
		C.Py_Initialize()
	}
	if C.Py_IsInitialized() == 0 {
		panic("python: could not initialize the python interpreter")
	}

	// make sure the Python threading facilities are correctly initialized,
	// please notice this will also lock the GIL, see [0] for reference.
	//
	// [0]: https://docs.python.org/2/c-api/init.html#c.PyEval_InitThreads
	if C.PyEval_ThreadsInitialized() == 0 {
		C.PyEval_InitThreads()
	}
	if C.PyEval_ThreadsInitialized() == 0 {
		panic("python: could not initialize the GIL")
	}

	// Set the PYTHONPATH if needed.
	// We still hold a lock from calling `C.PyEval_InitThreads()` above, so we can
	// safely use go-python here without any additional loking operation.
	if len(paths) > 0 {
		path := python.PySys_GetObject("path")
		for _, p := range paths {
			python.PyList_Append(path, python.PyString_FromString(p))
		}
	}

	// We acquired the GIL as a side effect of threading initialization (see above)
	// but from this point on we don't need it anymore. Let's reset the current thread
	// state and release the GIL, meaning that from now on any piece of code needing
	// Python needs to take care of thread state and the GIL on its own.
	// The previous thread state is returned to the caller so it can be stored and
	// reused when needed (e.g. to finalize the interpreter on exit).
	state := python.PyEval_SaveThread()

	// inject synthetic modules into the global namespace of the embedded interpreter
	// (all these calls will take care of the GIL)
	initAPI()          // `aggregator` module
	initDatadogAgent() // `datadog_agent` module

	// return the state so the caller can resume
	return state
}

// Search in module for a class deriving from baseClass and return the first match if any.
func findSubclassOf(base, module *python.PyObject) (*python.PyObject, error) {
	// Lock the GIL and release it at the end of the run
	gstate := NewStickyLock()
	defer gstate.Unlock()

	if base == nil || module == nil {
		return nil, fmt.Errorf("both base class and module must be not nil")
	}

	// baseClass is not a Class type
	if !python.PyType_Check(base) {
		return nil, fmt.Errorf("%s is not of Class type", python.PyString_AS_STRING(base.Str()))
	}

	// module is not a Module object
	if !python.PyModule_Check(module) {
		return nil, fmt.Errorf("%s is not a Module object", python.PyString_AS_STRING(module.Str()))
	}

	dir := module.PyObject_Dir()
	var class *python.PyObject
	for i := 0; i < python.PyList_GET_SIZE(dir); i++ {
		symbolName := python.PyString_AsString(python.PyList_GET_ITEM(dir, i))
		class = module.GetAttrString(symbolName)

		if !python.PyType_Check(class) {
			continue
		}

		// IsSubclass returns success if class is the same, we need to go deeper
		if class.IsSubclass(base) == 1 && class.RichCompareBool(base, python.Py_EQ) != 1 {
			return class, nil
		}
	}
	return nil, fmt.Errorf("cannot find a subclass of %s in module %s",
		python.PyString_AS_STRING(base.Str()), python.PyString_AS_STRING(base.Str()))
}

// Get the rightmost component of a module path like foo.bar.baz
func getModuleName(modulePath string) string {
	toks := strings.Split(modulePath, ".")
	// no need to check toks length, worst case it contains only an empty string
	return toks[len(toks)-1]
}

// GetInterpreterVersion should go in `go-python`, TODO.
func GetInterpreterVersion() string {
	// Lock the GIL and release it at the end of the run
	gstate := NewStickyLock()
	defer gstate.Unlock()

	res := C.Py_GetVersion()
	if res == nil {
		return ""
	}

	return C.GoString(res)
}
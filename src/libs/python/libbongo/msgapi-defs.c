#include <Python.h>
#include "msgapi-defs.h"

PyObject *
msgapi_GetUnprivilegedUser(PyObject *self, PyObject *args)
{
    return Py_BuildValue("z", MsgGetUnprivilegedUser());
}

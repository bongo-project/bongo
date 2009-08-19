#include <Python.h>

#include "libs.h"

#ifdef __cplusplus
extern "C" {
#endif

// to test this:
// from libbongo.libs import tests
// print tests.GetAsciiString()
// print tests.GetUnicodeString()

static PyObject *
test_GetAsciiString(PyObject *one, PyObject *two)
{
	char *hello_english = "Hello";
	PyObject *ret;

	ret = Py_BuildValue("s", hello_english);

	return ret;
}

static PyObject *
test_GetUnicodeString(PyObject *one, PyObject *two)
{
	char *hello_finnish = "hyvää päivää";
	PyObject *ret;

	ret = PyUnicode_Decode(hello_finnish, strlen(hello_finnish),
		"utf-8", "strict");

	return ret;
}

PyMethodDef TestMethods[] = {
	{"GetAsciiString", test_GetAsciiString, METH_VARARGS | METH_STATIC},
	{"GetUnicodeString", test_GetUnicodeString, METH_VARARGS | METH_STATIC},
	{NULL, NULL, 0, NULL}
};

#ifdef __cplusplus
}
#endif

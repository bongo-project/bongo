#include "pybongo.h"

#ifdef __cplusplus
extern "C" {
#endif

void
AddLibrary(PyObject *module, char *name, PyMethodDef *classMethods,
           EnumItemDef *enumItems)
{
    PyMethodDef *def;
    EnumItemDef *item;

    /* Create the new class object and add it to the module */    
    PyObject *moduleDict = PyModule_GetDict(module);
    PyObject *classDict = PyDict_New();
    PyObject *className = PyString_FromString(name);
    PyObject *classObj = PyClass_New(NULL, classDict, className);

    PyDict_SetItemString(moduleDict, name, classObj);

    /* Add the class's methods to its dict */
    for (def = classMethods; def->ml_name != NULL; def++) {
        PyObject *func = PyCFunction_New(def, NULL);
        PyObject *method;
        if (def->ml_flags & METH_STATIC) {
            method = PyStaticMethod_New(func);
        } else {
            method = PyMethod_New(func, NULL, classObj);
        }
        PyDict_SetItemString(classDict, def->ml_name, method);
        Py_DECREF(func);
        Py_DECREF(method);
    }

    if (enumItems != NULL) {
        for (item = enumItems; item->name != NULL; item++) {
            PyObject *value;
            if(item->strvalue) {
                value = PyString_FromString(item->strvalue);
            } else {
                value = PyInt_FromLong(item->value);
            }
            PyDict_SetItemString(classDict, item->name, value);
            Py_DECREF(value);
        }
    }

    Py_DECREF(classDict);
    Py_DECREF(className);
    Py_DECREF(classObj);
}

#ifdef __cplusplus
}
#endif

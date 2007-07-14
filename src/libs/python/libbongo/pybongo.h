#ifndef PYBONGO_H
#define PYBONGO_H

#include <Python.h>

#ifdef __cplusplus
extern "C" {
#endif


struct EnumItemDef {
    char *name;
    long value;
    char *strvalue;
};

typedef struct EnumItemDef EnumItemDef;

void AddLibrary(PyObject *, char *, PyMethodDef *, EnumItemDef *);

extern int BongoCalPostInit(PyObject *module);


#ifdef __cplusplus
}
#endif

#endif /* PYBONGO_H */

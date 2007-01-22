#ifndef PYBONGO_MSGAPI_DEFS_H
#define PYBONGO_MSGAPI_DEFS_H

#include "pybongo.h"
#include <bongo-config.h>
#include <xpl.h>
#include <msgapi.h>

#ifdef __cplusplus
extern "C" {
#endif

PyDoc_STRVAR(GetConfigProperty_doc,
"GetConfigProperty(s) -> string response\n\
\n\
Return the value for configuration property s..");
PyObject *msgapi_GetConfigProperty(PyObject *, PyObject *);

PyDoc_STRVAR(GetUnprivilegedUser_doc,
"GetUnprivilegedUser() -> string response\n\
\n\
Return the name of the unprivileged user if configured --with-user=.");
PyObject *msgapi_GetUnprivilegedUser(PyObject *, PyObject *);

#ifdef __cplusplus
}
#endif

#endif /* PYBONGO_MSGAPI_DEFS_H */

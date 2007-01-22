/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2001 Novell, Inc. All Rights Reserved.
 * 
 * This program is free software; you can redistribute it and/or
 * modify it under the terms of version 2 of the GNU General Public License
 * as published by the Free Software Foundation.
 * 
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 * 
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, contact Novell, Inc.
 * 
 * To contact Novell about this file by physical or electronic mail, you 
 * may find current contact information at www.novell.com.
 * </Novell-copyright>
 ****************************************************************************/

#include <config.h>
#include <xpl.h>

#include <bongoutil.h>
#include <msgapi.h>

#include "connmgrp.h"

void
ModulesVerify(ModuleStruct *first, ConnMgrCommand *command, ConnMgrResult *result)
{
    ModuleStruct *module = first;
    result->result = command->detail.policy.default_result;

    while (module) {
        if (module->registration.Verify) {
            int rc = module->registration.Verify(command, result);

            if (rc & CM_MODULE_DENY) {
                if (rc & CM_MODULE_PERMANENT) {
                    result->result = CM_RESULT_DENY_PERMANENT;
                } else {
                    result->result = CM_RESULT_DENY_TEMPORARY;
                }
            }

            if (rc & CM_MODULE_FORCE) {
                if (rc & CM_MODULE_ACCEPT) {
                    result->result = CM_RESULT_ALLOWED;
                }

                return;
            }
        }

        module = module->next;
    }
}

void
ModulesNotify(ModuleStruct *first, ConnMgrCommand *command)
{
    ModuleStruct *module = first;

    while (module) {
        if (module->registration.Notify) {
            module->registration.Notify(command);
        }

        module = module->next;
    }
}

ModuleStruct *
LoadModules(unsigned char *directory, ModuleStruct *first)
{
    XplDir *dir;
    XplDir *dirent;
    ModuleStruct *result = first;

    XplMakeDir(XPL_DEFAULT_DBF_DIR "//connmgr");

    if ((dir = XplOpenDir(directory)) != NULL) {
        while ((dirent = XplReadDir(dir)) != NULL) {
            unsigned char			path[XPL_MAX_PATH +1 ];
            unsigned char			name[XPL_MAX_PATH + 1];
            unsigned char           *modulename = dirent->d_nameDOS;
            ModuleStruct            *new = MemMalloc(sizeof(ModuleStruct));
            unsigned char           datadir[XPL_MAX_PATH + 1];

            memset(new, 0, sizeof(ModuleStruct));

            if (strlen(modulename) > 3) {
                snprintf(path, sizeof(path), "%s/lib%s", directory, modulename);
                new->handle = XplLoadDLL(path);

                if (!new->handle) {
                    snprintf(path, sizeof(path), "%s/%s", directory, modulename);
                    new->handle = XplLoadDLL(path);
                }

                if (new->handle) {
                    unsigned char *ptr;

                    /* Initialize the module */
                    if (XplStrNCaseCmp(modulename, "lib", 3) == 0) {
                        BongoStrNCpy(name, modulename + 3, sizeof(name) - strlen("Init"));
                    } else {
                        BongoStrNCpy(name, modulename, sizeof(name) - strlen("Init"));
                    }

                    ptr = strrchr(name, '.');
                    if (ptr) {
                        *ptr = '\0';
                    }

                    /* Setup the work directory for the module */
                    ptr = name;
                    while (*ptr) {
                        *ptr = tolower(*ptr);
                        ptr++;
                    }

                    sprintf(datadir, "%s/connmgr/%s", XPL_DEFAULT_DBF_DIR, name);
                    XplMakeDir(datadir);

                    ptr = name;
                    while (*ptr) {
                        *ptr = toupper(*ptr);
                        ptr++;
                    }

                    strcat(name, "Init");
                    new->Init = (CMInitFunc)XplGetDLLFunction(modulename, name, new->handle);
                    name[strlen(name) - 4] = '\0';
#if 0
                } else {
                    /* dlerror() is much more useful when dlopen fails than errno is */
                    unsigned char *err = dlerror();
                    if (err) {
                        XplConsolePrintf("connmgr: XplLoadDLL failed: %s\n", err);
                    }
                    XplConsolePrintf("connmgr: could not load module %s/%s, errno: %d %s\n", directory, modulename, errno, strerror(errno));
#endif
                }
            }

            if (new->Init && new->Init(&(new->registration), datadir)) {
                new->name = MemStrdup(name);

#if VERBOSE
                XplConsolePrintf("connmgr: loaded module: %s\n", new->name);
#endif

                if (!result) {
                    result = new;
                } else if (result->registration.priority > new->registration.priority) {
                    new->next = result;
                    result = new;
                } else {
                    ModuleStruct *n = result;

                    /* Find a spot for it */
                    while (n && n->next) {
                        if (n->next->registration.priority > new->registration.priority) {
                            new->next = n->next;
                            n->next = new;
                            n = NULL;
                        }

                        if (n) {
                            n = n->next;
                        }
                    }

                    if (n) {
                        n->next = new;
                    }
                }
            } else {
                MemFree(new);
            }
        }

        XplCloseDir(dir);
    }

    return(result);
}

void
UnloadModules(ModuleStruct *first)
{
    ModuleStruct *module = first;

    while (module) {
        ModuleStruct *next = module->next;

        if (module->registration.Shutdown) {
            module->registration.Shutdown();
        }

        if (module->handle && module->name) {
            XplUnloadDLL(module->name, module->handle);
            MemFree(module->name);
        }

        MemFree(module);
        module = next;
    }
}

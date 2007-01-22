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

#include <connmgr.h>

#define CONNMGR_STACK_SPACE (1024 * 32)
#define CONNECTION_TIMEOUT (15 * 60)

/* modules.c */
typedef struct _ModuleStruct {
    struct _CMModuleRegistrationStruct registration;

    CMInitFunc Init;
    XplPluginHandle handle;
    struct _ModuleStruct *next;
    unsigned char *name;
} ModuleStruct;


void ModulesVerify(ModuleStruct *first, ConnMgrCommand *command, ConnMgrResult *result);
void ModulesNotify(ModuleStruct *first, ConnMgrCommand *command);
ModuleStruct * LoadModules(unsigned char *directory, ModuleStruct *first);
void UnloadModules(ModuleStruct *first);


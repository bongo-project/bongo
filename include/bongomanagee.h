/****************************************************************************
 * <Novell-copyright>
 * Copyright (c) 2006 Novell, Inc. All Rights Reserved.
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

#ifndef BONGOMANAGEE_H_
#define BONGOMANAGEE_H_

#include <xpl.h>
#include <bongojson.h>
#include <bongojsonrpc.h>
#include <connio.h>

typedef struct _BongoManageeVariable BongoManageeVariable;
typedef struct _BongoManagee BongoManagee;

typedef BongoJsonNode *(*BongoManageeReadFunc) (BongoManagee *managee,
                                              BongoManageeVariable *variable);
typedef void (*BongoManageeWriteFunc) (BongoManagee *managee, 
                                      BongoManageeVariable *variable,
                                      BongoJsonNode *value);

struct _BongoManageeVariable {
    int id;
    char *name;
    BongoManageeReadFunc read;
    BongoManageeWriteFunc write;
    void *userData;
};

BongoManagee *BongoManageeNew (const char *agentName, 
                             const char *agentDn, 
                             const char *agentVersion);

int BongoManageeAddVariable (BongoManagee *monitor,
                            int id,
                            const char *name, 
                            BongoManageeReadFunc readFunc,
                            BongoManageeWriteFunc writeFunc,
                            void *userData);

void BongoManageeAddVariables (BongoManagee *managee,
                              BongoManageeVariable *variables);

void BongoManageeAddVariablesConst (BongoManagee *managee,
                                   BongoManageeVariable *variables);

void BongoManageeAddMethod (BongoManagee *managee, 
                           const char *name,
                           BongoJsonRpcMethodFunc func,
                           void *userData);

/* Simple default functions */

BongoJsonNode *BongoManageeReadString (BongoManagee *managee, BongoManageeVariable *variable);
BongoJsonNode *BongoManageeReadAtomic (BongoManagee *managee, BongoManageeVariable *variable);
BongoJsonNode *BongoManageeReadPoolStats (BongoManagee *managee, BongoManageeVariable *variable);
BongoJsonNode *BongoManageeReadMemoryStats (BongoManagee *managee, BongoManageeVariable *variable);

/* Client API */
BongoJsonNode *BongoManageeGet (BongoManagee *managee,
                              const char *name);

/* Server/Listener API */
BongoJsonRpcServer *BongoManageeCreateServer (BongoManagee *managee);

#endif /*BONGOMANAGEE_H_*/

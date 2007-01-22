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

#include <config.h>
#include <bongomanagee.h>

#include <xpl.h>

#include <memmgr.h>
#include <bongoutil.h>
#include <assert.h>
#include <msgapi.h>

#define USE_DOMAIN_SOCKETS 1

typedef struct {
    BongoJsonRpcMethodFunc func;
    char *name;
    void *userData;
} ManageeMethod;

struct _BongoManagee {
    int assignId;
    const char *agentName;
    const char *agentDn;
    
    BongoHashtable *variables;
    BongoHashtable *methods;
};

BongoManagee *
BongoManageeNew (const char *agentName, const char *agentDn, const char *agentVersion)
{
    BongoManagee *managee;
    
    managee = MemMalloc (sizeof (BongoManagee)); 
    
    managee->assignId = 1000;
    managee->agentName = MemStrdup (agentName);
    managee->agentDn = MemStrdup (agentDn);
    managee->variables = BongoHashtableCreate(16, (HashFunction)BongoStringHash, (CompareFunction)strcmp);
    managee->methods = BongoHashtableCreate(16, (HashFunction)BongoStringHash, (CompareFunction)strcmp);

    return managee;
}

int 
BongoManageeAddVariable (BongoManagee *managee,
                        int id,
                        const char *name, 
                        BongoManageeReadFunc readFunc,
                        BongoManageeWriteFunc writeFunc,
                        void *userData)
{
    BongoManageeVariable *var;
    
    var = MemMalloc0 (sizeof (BongoManageeVariable));
    
    if (id == -1) {
        id = managee->assignId++; 
    }
    var->id = id;
    
    var->name = MemStrdup (name);
    var->read = readFunc;
    var->write = writeFunc;
    var->userData = userData;
    
    BongoHashtablePut (managee->variables, var->name, var);
    
    return var->id;
}

void
BongoManageeAddVariables (BongoManagee *managee,
                         BongoManageeVariable *variables)
{
    BongoManageeVariable *p;
    
    for (p = variables; p->name != NULL; p++) {
        p->id = BongoManageeAddVariable (managee, 
                                        p->id,
                                        p->name,
                                        p->read,
                                        p->write,
                                        p->userData);
    }
}

void
BongoManageeAddVariablesConst (BongoManagee *managee,
                              BongoManageeVariable *variables)
{
    BongoManageeVariable *p;
    
    for (p = variables; p->name != NULL; p++) {
        BongoHashtablePut (managee->variables, p->name, p);
    }
}

void
BongoManageeAddMethod (BongoManagee *managee,
                      const char *name,
                      BongoJsonRpcMethodFunc func,
                      void *userData)
{
    ManageeMethod *method;
    
    method = MemMalloc0 (sizeof (ManageeMethod));
    method->name = MemStrdup (name);
    method->func = func;
    method->userData = userData;
    
    BongoHashtablePut (managee->methods, method->name, method);
}

/* Variable helpers */

BongoJsonNode *
BongoManageeReadString (BongoManagee *managee, BongoManageeVariable *variable)
{
    return BongoJsonNodeNewString (variable->userData);
}

BongoJsonNode *
BongoManageeReadAtomic (BongoManagee *managee, BongoManageeVariable *variable)
{
    XplAtomic *atomic = variable->userData;
    return BongoJsonNodeNewInt (XplSafeRead(*atomic));
}


static BongoJsonObject *
PoolStatsToObj (MemStatistics *stats)
{
    BongoJsonObject *obj;
    
    obj = BongoJsonObjectNew ();
    
    if (!obj ||
        BongoJsonObjectPutInt (obj, "count", stats->totalAlloc.count) != BONGO_JSON_OK ||
        BongoJsonObjectPutInt (obj, "size", stats->totalAlloc.size) != BONGO_JSON_OK ||
        BongoJsonObjectPutInt (obj, "pitches", stats->pitches) != BONGO_JSON_OK ||
        BongoJsonObjectPutInt (obj, "strikes", stats->strikes) != BONGO_JSON_OK) {
        
        BongoJsonObjectFree (obj);
        return NULL;
    }

    return obj;
}

BongoJsonNode *
BongoManageeReadPoolStats (BongoManagee *managee, BongoManageeVariable *variable)
{
    MemStatistics stats = { { 0 }, };
    BongoJsonObject *obj;
    
    MemPrivatePoolStatistics(variable->userData, &stats);
    
    obj = PoolStatsToObj (&stats);

    return BongoJsonNodeNewObject (obj);
}

BongoJsonNode *
BongoManageeReadMemoryStats (BongoManagee *managee, BongoManageeVariable *variable)
{
    MemStatistics *stats;
    MemStatistics *p;
    BongoArray *ret;
    
    stats = MemAllocStatistics();
    if (!stats) {
        return NULL;
    }

    ret = BongoJsonArrayNew (16);
    
    if (!ret) {
        return NULL;
    }
    
    for (p = stats; p != NULL; p = p->next) {
        BongoJsonObject *obj;
        obj = PoolStatsToObj (p);
        if (!obj) {
            BongoJsonArrayFree (ret);
            MemFreeStatistics (stats);
            return NULL;
        }
    }

    MemFreeStatistics (stats);
    
    return BongoJsonNodeNewArray (ret);
}

BongoJsonNode *
BongoManageeGet (BongoManagee *managee,
                const char *name)
{
    BongoManageeVariable *var;
    
    var = BongoHashtableGet (managee->variables, name);
    
    if (!var || !var->read) {
        return FALSE;
    }

    return var->read (managee, var);
}

/* json-rpc server */



static void
GetValuesFunc (BongoJsonRpcServer *server,
               BongoJsonRpc *rpc, 
               int requestId,
               const char *method,
               BongoArray *params,
               void *userData)
{
    BongoManagee *managee = userData;
    const char *varName;
    BongoManageeVariable *var;
    BongoJsonObject *ret;
    unsigned int i;
    
    if (params->len < 1) {
        BongoJsonRpcError (rpc, requestId, 
                          BongoJsonNodeNewString ("At least one argument expected."));
    }
    
    ret = BongoJsonObjectNew ();
    
    for (i = 0; i < params->len; i++) {
        BongoJsonNode *value;
        BongoJsonNode *error;
        BongoJsonObject *result;
        
        if (BongoJsonArrayGetString (params, i, &varName) != BONGO_JSON_OK) {
            BongoJsonRpcError (rpc, requestId, 
                              BongoJsonNodeNewString ("Arguments must be a string."));
            BongoJsonObjectFree (ret);
            return;
        }
        
        var = BongoHashtableGet (managee->variables, varName);
        if (!var) {
            error = BongoJsonNodeNewString ("Variable not found.");
            value = BongoJsonNodeNewNull ();
            goto varDone;
        }
        
        if (!var->read) {
            error = BongoJsonNodeNewString ("Variable is not readable.");
            value = BongoJsonNodeNewNull ();
            goto varDone;
        }
    
        value = var->read (managee, var);
        if (!value) {
            error = BongoJsonNodeNewString ("Error reading value.");
            value = BongoJsonNodeNewNull ();
        } else {
            error = BongoJsonNodeNewNull ();
        }

varDone :
        result = BongoJsonObjectNew ();
        if (BongoJsonObjectPut (result, "error", error) != BONGO_JSON_OK) {
            
            BongoJsonNodeFree (error);
            BongoJsonNodeFree (value);
            BongoJsonObjectFree (result);
            BongoJsonObjectFree (ret);
            BongoJsonRpcError (rpc, requestId, BongoJsonNodeNewString ("Couldn't create result object"));
            return;
        }
        
        if (BongoJsonObjectPut (result, "result", value) != BONGO_JSON_OK) {            
            BongoJsonNodeFree (value);
            BongoJsonObjectFree (result);
            BongoJsonObjectFree (ret);
            BongoJsonRpcError (rpc, requestId, BongoJsonNodeNewString ("Couldn't create result object"));
            return;
        }
        
        if (BongoJsonObjectPutObject (ret, varName, result) != BONGO_JSON_OK) {
            BongoJsonObjectFree (result);
            BongoJsonObjectFree (ret);
            BongoJsonRpcError (rpc, requestId, BongoJsonNodeNewString ("Couldn't create result object"));
            return;            
        }
              
    }
    
    /* Send a response */
    BongoJsonRpcSuccess (rpc, requestId, BongoJsonNodeNewObject (ret));
}

static void
SetValueFunc (BongoJsonRpcServer *server,
              BongoJsonRpc *rpc, 
              int requestId,
              const char *method,
              BongoArray *params,
              void *userData)
{
    BongoManagee *managee = userData;
    const char *varName;
    BongoManageeVariable *var;
    BongoJsonNode *value;

    if (params->len != 2) {
        BongoJsonRpcError (rpc, requestId, 
                          BongoJsonNodeNewString ("2 arguments expected."));
    }
    
    if (BongoJsonArrayGetString (params, 0, &varName) != BONGO_JSON_OK) {
        BongoJsonRpcError (rpc, requestId, 
                          BongoJsonNodeNewString ("First argument must be a string."));
        return;
    }
    
    value = BongoJsonArrayGet (params, 1);
    
    var = BongoHashtableGet (managee->variables, varName);
    if (!var) {
        BongoJsonRpcError (rpc, requestId,
                          BongoJsonNodeNewString ("Variable not found."));
        return;
    }
    
    if (!var->write) {
        BongoJsonRpcError (rpc, requestId,
                          BongoJsonNodeNewString ("Variable is not writable."));
        return;        
    }
    
    var->write (managee, var, value);

    BongoJsonRpcSuccess (rpc, requestId, BongoJsonNodeNewString ("Success"));
}

static BOOL
NewConnection (BongoJsonRpcServer *server,
               BongoJsonRpc *rpc)
{
    /* FIXME: Check auth on the socket */
    return TRUE;
}

BongoJsonRpcServer *
BongoManageeCreateServer (BongoManagee *managee)
{
    Connection *conn;
    BongoJsonRpcServer *server;
    BongoHashtableIter iter;
    char path[XPL_MAX_PATH];
        
    conn = ConnAlloc(TRUE);
    if (!conn) {
        return NULL;
    }    
    
#if USE_DOMAIN_SOCKETS
    MsgGetDBFDir (path);
    strncat (path, "/dmc/", sizeof (path) - strlen (path) - 1);
    if (XplMakeDir (path) == -1 && errno != EEXIST) {
        ConnFree (conn);
        return NULL;
    }

    strncat (path, managee->agentName, sizeof (path) - strlen (path) - 1);
    unlink (path);
    conn->socket = ConnServerSocketUnix (conn, path, 5);
#else
    conn->socketAddress.sin_family = AF_INET;
    conn->socketAddress.sin_addr.s_addr = MsgGetAgentBindIPAddress();
    conn->socketAddress.sin_port = htons (6666);
    conn->socket = ConnServerSocket (conn, 5);
#endif

    if (conn->socket == -1) {
        ConnFree (conn);
        return NULL;
    }

    server = BongoJsonRpcServerNew (conn);
    if (!server) {
        ConnClose (conn, 0);
        ConnFree (conn);
                
        return NULL;
    }
    
    server->connectionHandler = NewConnection;
    
    for (BongoHashtableIterFirst (managee->methods, &iter);
         iter.key != NULL;
         BongoHashtableIterNext (managee->methods, &iter)) {
        ManageeMethod *method = iter.value;
        BongoJsonRpcServerAddMethod(server, iter.key, method->func, method->userData);
    }
    
//    BongoJsonRpcServerAddMethod(server, "getValue", GetValueFunc, managee);
    BongoJsonRpcServerAddMethod(server, "getValues", GetValuesFunc, managee);
                
    return server;
}

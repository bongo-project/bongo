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

#ifndef BONGOJSONRPC_H_
#define BONGOJSONRPC_H_

#include <xpl.h>
#include <bongojson.h>
#include <connio.h>
#include <glib.h>

typedef struct _BongoJsonRpc BongoJsonRpc;
typedef struct _BongoJsonRpcServer BongoJsonRpcServer;


typedef void (*BongoJsonRpcMethodFunc) (BongoJsonRpcServer *server,
                                       BongoJsonRpc *rpc,
                                       int requestId,
                                       const char *method, 
                                       GArray *params,
                                       void *userData);

typedef BOOL (*BongoJsonRpcConnectionFunc) (BongoJsonRpcServer *server,
                                           BongoJsonRpc *rpc);
typedef void (*BongoJsonRpcDestroyFunc) (BongoJsonRpc *rpc);

struct _BongoJsonRpc {
    Connection *conn;
    BongoJsonRpcDestroyFunc destroyHandler;
    
    BongoJsonParser *parser;
    
    void *userData;
};

struct _BongoJsonRpcServer {
    Connection *listener;
    BongoJsonRpcConnectionFunc connectionHandler;
    
    BongoHashtable *methods;

    BOOL running;
    void *userData;
};

/* === Client Side === */
BongoJsonRpc *BongoJsonRpcNew (Connection *conn);
void BongoJsonRpcFree (BongoJsonRpc *rpc);

BongoJsonObject *BongoJsonRpcRequest (BongoJsonRpc *rpc, 
                                    const char *method, 
                                    GArray *params);

/* === Server Side === */
BongoJsonRpcServer *BongoJsonRpcServerNew (Connection *listener);

void BongoJsonRpcServerRun (BongoJsonRpcServer *server, BOOL spawnThread);

/* Handle a previously-accepted connection */
void BongoJsonRpcServerHandleConnection (BongoJsonRpcServer *server, Connection *conn);

void BongoJsonRpcServerAddMethod (BongoJsonRpcServer *server,
                                 const char *method,
                                 BongoJsonRpcMethodFunc handler,
                                 void *userData);

int BongoJsonRpcSuccess (BongoJsonRpc *rpc,
                        int requestId, 
                        BongoJsonNode *result);

int BongoJsonRpcError (BongoJsonRpc *rpc,
                      int requestId,
                      BongoJsonNode *result);

#endif /*BONGOJSONRPC_H_*/

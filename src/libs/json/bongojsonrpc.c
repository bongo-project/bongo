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
#include <bongojsonrpc.h>
#include <memmgr.h>
#include <assert.h>

#define STACKSIZE (128 * 124)

typedef struct {
    BongoJsonRpcMethodFunc func;
    void *userData;
} MethodHandler;

BongoJsonRpc *
BongoJsonRpcNew (Connection *conn)
{
    BongoJsonRpc *rpc;
    
    rpc = MemMalloc0 (sizeof (BongoJsonRpc));
    rpc->conn = conn;
    
    rpc->parser = BongoJsonParserNew (rpc->conn, (BongoJsonParserReadFunc)ConnRead, NULL, 0);
    
    return rpc;
}

void
BongoJsonRpcFree (BongoJsonRpc *rpc)
{
    if (rpc->destroyHandler) {
        rpc->destroyHandler (rpc);
    }
    
    BongoJsonParserFree (rpc->parser);
    
    ConnClose (rpc->conn);
    ConnFree (rpc->conn);
    
    MemFree (rpc);
}

BongoJsonRpcServer *
BongoJsonRpcServerNew (Connection *listener)
{
    BongoJsonRpcServer *server;
    
    server = MemMalloc0 (sizeof (BongoJsonRpcServer));
    server->listener = listener;
    server->methods = BongoHashtableCreateFull(16, 
                                              (HashFunction)BongoStringHash, 
                                              (CompareFunction)strcmp, 
                                               MemFreeDirect, MemFreeDirect);
    
    return server;
}

static void
Listener (void *data)
{
    BongoJsonRpcServer *server = data;
        
    while (server->running) {
        Connection *conn;
        
        if (ConnAccept (server->listener, &conn) != -1) {
            BongoJsonRpcServerHandleConnection (server, conn);           
        } else {
            switch (errno) {
                case ECONNABORTED:
#ifdef EPROTO
                case EPROTO: 
#endif
                case EINTR: {
                    continue;
                }

                default: {
                    return;
                }

                break;
            }
        }
    }
}

void
BongoJsonRpcServerRun (BongoJsonRpcServer *server, BOOL spawnThread)
{
    server->running = TRUE;
    if (spawnThread) {
        XplThreadID id;
        int ret;
        
        XplBeginThread(&id, Listener, STACKSIZE, server, ret);
    } else {
        Listener (server);
    }
}

static void
HandleRequest (BongoJsonRpcServer *server, BongoJsonRpc *rpc, BongoJsonObject *request)
{
    const char *method;
    GArray *params;
    int requestId;
    MethodHandler *handler;
    
    if (BongoJsonObjectGetString (request, "method", &method) != BONGO_JSON_OK ||
        BongoJsonObjectGetArray (request, "params", &params) != BONGO_JSON_OK ||
        BongoJsonObjectGetInt (request, "id", &requestId) != BONGO_JSON_OK) {
        BongoJsonRpcError (rpc, 0, BongoJsonNodeNewString ("Invalid request"));
        return;
    }
    
    /* Not locking server->handlers, it shouldn't be modified after the server 
     * starts */
    handler = BongoHashtableGet (server->methods, method);
    if (!handler) {
        BongoJsonRpcError (rpc, requestId, BongoJsonNodeNewString ("Method not found"));
        return;    
    }
    
    handler->func (server, rpc, requestId, method, params, handler->userData); 
}

typedef struct {
    BongoJsonRpcServer *server;
    Connection *conn;
} ConnectionData;

static void
HandleConnection (void *datap)
{
    ConnectionData *data = datap; 
    BongoJsonRpcServer *server = data->server;
    Connection *conn = data->conn;
    BongoJsonRpc *rpc;
    BongoJsonResult res;
    BongoJsonNode *node;
    
    MemFree (data);

    rpc = BongoJsonRpcNew (conn);
    if (server->connectionHandler) {
        if (!server->connectionHandler (server, rpc)) {
            BongoJsonRpcFree (rpc);
            return;
        }
    }
    
    rpc->parser = BongoJsonParserNew(conn, (BongoJsonParserReadFunc)ConnRead, NULL, 0);
    
    while ((res = BongoJsonParserReadNode (rpc->parser, &node)) == BONGO_JSON_OK) {
        if (node->type == BONGO_JSON_OBJECT) {
            HandleRequest (server, rpc, BongoJsonNodeAsObject (node));
        }
                
        BongoJsonNodeFree (node);        
    }
    
    BongoJsonParserFree (rpc->parser);
    
    BongoJsonRpcFree (rpc);    

}

/* Handle a previously-accepted connection */
void
BongoJsonRpcServerHandleConnection (BongoJsonRpcServer *server, Connection *conn)
{
    XplThreadID id;
    int ret;
    ConnectionData *data;
    
    data = MemMalloc0 (sizeof (ConnectionData));
    data->server = server;
    data->conn = conn;
    
    XplBeginThread(&id, HandleConnection, STACKSIZE, data, ret);
}

void
BongoJsonRpcServerAddMethod (BongoJsonRpcServer *server,
                            const char *method, /* NULL for the default handler */
                            BongoJsonRpcMethodFunc func,
                            void *userData)
{
    MethodHandler *handler;
    
    assert (!server->running);
    
    handler = MemMalloc0 (sizeof (MethodHandler));
    if (!handler) {
        return;
    }
    
    handler->func = func;
    handler->userData = userData;
    
    BongoHashtablePut (server->methods, MemStrdup(method), handler);
}

/* If successful, result and error are owned by the object */
static BongoJsonObject *
BuildResponse (int requestId, 
               BongoJsonNode *result,
               BongoJsonNode *error)
{
    BongoJsonObject *ret = NULL;
    
    ret = BongoJsonObjectNew ();
    if (!ret) {
        goto error;
    }
    
    if (BongoJsonObjectPutInt (ret, "id", requestId) != BONGO_JSON_OK ||
        BongoJsonObjectPut (ret, "result", result) != BONGO_JSON_OK ||
        BongoJsonObjectPut (ret, "error", error) != BONGO_JSON_OK) {        
        goto error;
    }
    
    return ret;

error:
    if (ret) {
        BongoJsonObjectRemoveSteal (ret, "result");
        BongoJsonObjectRemoveSteal (ret, "error");
        BongoJsonObjectFree (ret);
    }

    return NULL;
}

/* Consumes result and error */
static int 
SendResponse (BongoJsonRpc *rpc,
              int requestId,
              BongoJsonNode *result,
              BongoJsonNode *error)
{
    BongoJsonObject *response;
    char *str;
    
    response = BuildResponse (requestId, result, error);
    if (!response) {
        BongoJsonNodeFree (result);
        BongoJsonNodeFree (error);
        return -1;
    }

    /* result and error are now owned by response */
    str = BongoJsonObjectToString (response);
    BongoJsonObjectFree (response);
    
    if (!str) {
        return -1;                  
    }
    
    if (ConnWriteStr (rpc->conn, str) == -1 ||
        ConnFlush (rpc->conn) == -1) {
        return -1;
    }
    
    return 0;    
}

int 
BongoJsonRpcSuccess (BongoJsonRpc *rpc,
                    int requestId, 
                    BongoJsonNode *result)
{
    return SendResponse (rpc, requestId, result, BongoJsonNodeNewNull());
}

int
BongoJsonRpcError (BongoJsonRpc *rpc,
                  int requestId,
                  BongoJsonNode *result)
{
    return SendResponse (rpc, requestId, BongoJsonNodeNewNull(), result);
}

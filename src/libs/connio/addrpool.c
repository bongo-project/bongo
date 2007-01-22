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
#include <memmgr.h>

#include <connio.h>
#include <stdio.h>


/* the pool write lock must be held when calling AddressPoolWorkTableCreate */
__inline static unsigned long **
AddressPoolWorkTableCreate(unsigned long addressCount, unsigned long product)
{
    unsigned long **table;
    unsigned long i;
    unsigned long j;

    /* Allocate the tables */
    table = (unsigned long **)MemCalloc(addressCount, sizeof(unsigned long *));
    if (!table) {
        return(NULL);
    }
    
    for(i = 0; i < addressCount; i++) {
        table[i] = (unsigned long *)MemCalloc(product, sizeof(unsigned long));
        if (!table[i]) {
            for(j = 0; j < i; j++) {
                MemFree(table[j]);
            }
            MemFree(table);
            return(NULL);
        }
    }
    return(table);
}

/* the pool write lock must be held when calling AddressPoolWorkTableFree */
__inline static void
AddressPoolWorkTableFree(unsigned long **table, unsigned long addressCount)
{
    unsigned long j;

    for(j = 0; j < addressCount; j++) {
        MemFree(table[j]);
    }
    MemFree(table);
}

/* the pool write lock must be held when calling AddressPoolWeightTableUpdate */
__inline static BOOL
AddressPoolWeightTableUpdate(AddressPool *pool)
{
    unsigned long i;
    unsigned long j;
    unsigned long k;
    BOOL lastInstance;
    unsigned long product;
    unsigned long **workTable;
    unsigned long jump;

    if (pool->weightTable.elements) {
        MemFree(pool->weightTable.elements);
        pool->weightTable.elements = NULL;
    }

    pool->weightTable.count = 0;

    if (pool->addressCount < 2) {
        return(TRUE);
    }

    /* Figure out how big the work and resulting tables need to be */
    product = 1;

    for (i = 0; i < pool->addressCount; i++) {
        /* the number of elements in the resulting weight table will simply be the sum of all the weights */ 
        pool->weightTable.count += pool->address[i].weight;
        lastInstance = TRUE;
        for (j = i + 1; j < pool->addressCount; j++) {
            if (pool->address[i].weight != pool->address[j].weight) {
                continue;
            }
            lastInstance = FALSE;
            break;
        }
        if (lastInstance) {
            /* the number of elements in the work tables will be the product of all the unique weight values */ 
            product *= pool->address[i].weight;
        }
    }

    /* Allocate the tables */
    workTable = AddressPoolWorkTableCreate(pool->addressCount, product);
    if (!workTable) {
        return(FALSE);
    }
    pool->weightTable.elements = MemCalloc(pool->weightTable.count, sizeof(unsigned long));
    if (!pool->weightTable.elements) {
        AddressPoolWorkTableFree(workTable, pool->addressCount);
        return(FALSE);        
    }

    /* populate the work table */
    for (i = 0; i < pool->addressCount; i++) {
        jump = product / pool->address[i].weight;
        for (j = 0; j < product;) {
            workTable[i][j] = 1;
            j += jump;
        }
    }

    /* populate the final table */
    k = 0;
    for (j = 0; j < product; j++) {
        for (i = 0; i < pool->addressCount; i++) {
            if (workTable[i][j] == 1) {
                pool->weightTable.elements[k] = i;
                k++;
            }
        }
    }

    AddressPoolWorkTableFree(workTable, pool->addressCount);
    return(TRUE);
}

/* the pool write lock must be held when calling AddressPoolRemove */
__inline static void
AddressPoolRemove(AddressPool *pool, long addrNum)
{
    memcpy(&(pool->address[addrNum]), &(pool->address[addrNum + 1]), sizeof(AddressPoolAddress) * (pool->addressCount - addrNum - 1));
    pool->addressCount--;
}

/* addr and port are expected to be in network byte order when they get passed to AddressPoolFind() */
/* the pool read lock must be held when calling AddressPoolFind */
__inline static long
AddressPoolFind(AddressPool *pool, uint32_t addr, unsigned short port)
{
    unsigned long i;

    for (i = 0; i < pool->addressCount; i++) {
        if (addr == pool->address[i].addr.sin_addr.s_addr) {
            if ((port == 0) || (port == pool->address[i].addr.sin_port)) {
                return((long)i);
            }
        }
    }
    return(-1);
}


/* the pool read lock must be held when calling AddressPoolFindById */
__inline static long
AddressPoolFindById(AddressPool *pool, unsigned long id)
{
    unsigned long i;

    for (i = 0; i < pool->addressCount; i++) {
        if (id == pool->address[i].id) {
            return((long)i);
        }
    }
    return(-1);
}

/* addr and port are expected to be in network byte order when they get passed to AddressPoolAdd() */
/* the pool write lock must be held when calling AddressPoolAdd */
/* pool->address must be of size pool->addressCount before calling AddressPoolAdd() */
__inline static void 
AddressPoolAdd(AddressPool *pool, uint32_t addr, unsigned short port, unsigned long weight)
{
    pool->address[pool->addressCount].addr.sin_family = AF_INET;
    pool->address[pool->addressCount].addr.sin_addr.s_addr = addr;
    pool->address[pool->addressCount].addr.sin_port = port;
    pool->address[pool->addressCount].weight = weight;
    pool->address[pool->addressCount].id = pool->nextId;
    pool->nextId++;
    XplSafeWrite(pool->address[pool->addressCount].errorCount, 0);
    pool->addressCount++;
}

/* address and port are expected to be in network byte order when they get passed to AddressPoolAddressAdd() */
/* the pool lock must NOT be held when calling AddressPoolAddressAdd */
__inline static BOOL
AddressPoolAddressAdd(AddressPool *pool, uint32_t ipAddress, unsigned short port, unsigned long weight)
{
    AddressPoolAddress *newAddrList;
    long ipNum;

    XplRWWriteLockAcquire(&(pool->lock));

    /* check to see if the ip is already in the list */
    ipNum = AddressPoolFind(pool, ipAddress, port);
    if (ipNum > -1) {
        /* we are removing an ip so we don't need to allocate a spot for the new one */
        AddressPoolRemove(pool, ipNum);
        AddressPoolAdd(pool, ipAddress, port, weight);
        AddressPoolWeightTableUpdate(pool);
        pool->version++;
        XplRWWriteLockRelease(&(pool->lock));
        return(TRUE);
    }

    newAddrList = MemMalloc(sizeof(AddressPoolAddress) * pool->addressCount + 1);
    if (!newAddrList) {
        XplRWWriteLockRelease(&(pool->lock));
        return(FALSE);

    }

    if (pool->addressCount > 0) {
        /* copy pre-existing hosts */
        memcpy(newAddrList, pool->address, sizeof(AddressPoolAddress) * pool->addressCount);

    }

    if (pool->address) {
        MemFree(pool->address);
    }

    pool->address = newAddrList;
    AddressPoolAdd(pool, ipAddress, port, weight);
    AddressPoolWeightTableUpdate(pool);
    pool->version++;
    XplRWWriteLockRelease(&(pool->lock));
    return(TRUE);
}

/* the pool write lock must be held when calling AddressPoolRemoveByIndex */
__inline static void
AddressPoolRemoveByIndex(AddressPool *pool, unsigned long ipNum)
{
    AddressPoolRemove(pool, ipNum);

    if (pool->addressCount == 0) {
        if (pool->address) {
            MemFree(pool->address);
            pool->address = NULL;
        }
        if (pool->weightTable.elements) {
            MemFree(pool->weightTable.elements);
            pool->weightTable.elements = NULL;
            pool->weightTable.count = 0;
        }
    }
}

/* the pool lock must NOT be held when calling AddressPoolConnectErrorAddressRemove */
__inline static void
AddressPoolConnectErrorAddressRemove(AddressPool *pool, unsigned long addressId)
{
    long ipNum;

    XplRWWriteLockAcquire(&(pool->lock));

    ipNum = AddressPoolFindById(pool, addressId);
    if (ipNum > -1) {
        AddressPoolRemoveByIndex(pool, ipNum);
        AddressPoolWeightTableUpdate(pool);
        pool->version++;
    }

    XplRWWriteLockRelease(&(pool->lock));
    return;
}

/* the pool lock must NOT be held when calling AddressPoolConnectError */
/* AddressPoolConnectError returns the number of times retries should occur */
__inline static unsigned long
AddressPoolConnectError(AddressPool *pool, unsigned long addressId)
{
    long ipNum;
    unsigned long retryLimit;

    XplRWReadLockAcquire(&(pool->lock));
    if (pool->addressCount == 1) {
        retryLimit = pool->address[0].weight;
    } else if (pool->addressCount > 1) {
        retryLimit = pool->weightTable.count;
    } else {
        retryLimit = 0;
    }

    ipNum = AddressPoolFindById(pool, addressId);
    if (ipNum > -1) {
        time_t currentTime;

        if (pool->errorThreshold == 0) {
            XplSafeIncrement(pool->address[ipNum].errorCount);
            XplRWReadLockRelease(&(pool->lock));
            return(retryLimit);
        }
            
        currentTime = time(NULL);
        if ((currentTime - pool->address[ipNum].lastErrorTime) < (long)pool->errorTimeThreshold) {
            XplSafeIncrement(pool->address[ipNum].errorCount);
        } else {
            XplSafeWrite(pool->address[ipNum].errorCount, 1);
        }
        pool->address[ipNum].lastErrorTime = currentTime;

        if ((unsigned long)XplSafeRead(pool->address[ipNum].errorCount) < pool->errorThreshold) {
            XplRWReadLockRelease(&(pool->lock));
            return(retryLimit);
        }

        XplRWReadLockRelease(&(pool->lock));
        AddressPoolConnectErrorAddressRemove(pool, addressId);
        return(retryLimit);
    }
    XplRWReadLockRelease(&(pool->lock));
    return(retryLimit);
}

__inline static unsigned long 
AddressPoolGetNextAddress(AddressPool *pool)
{
    if (pool->addressCount == 1) {
        return(0);
    } else {
        unsigned long index;
        
        XplMutexLock(pool->weightTable.mutex);
        index = pool->weightTable.index;
        pool->weightTable.index++;
        if (pool->weightTable.index >= pool->weightTable.count) {
            pool->weightTable.index = 0;
        }
        XplMutexUnlock(pool->weightTable.mutex);
        return(pool->weightTable.elements[index]);
    }
}

/* the pool lock must NOT be held when calling AddressPoolConnect */
__inline static BOOL
AddressPoolConnect(AddressPool *pool, Connection *conn, unsigned long timeOut)
{
    unsigned long attempts = 0;
    unsigned long attemptLimit;
    unsigned long addressId;
    
    for (;;) {
        XplRWReadLockAcquire(&(pool->lock));
        if (pool->addressCount > 0) {
            unsigned long addrIdx;

            addrIdx = AddressPoolGetNextAddress(pool);
            addressId = pool->address[addrIdx].id;
            memcpy(&conn->socketAddress, &pool->address[addrIdx].addr, sizeof(struct sockaddr_in));
            
            XplRWReadLockRelease(&(pool->lock));
            if (ConnConnectWithTimeOut(conn, NULL, 0, NULL, NULL, timeOut) >= 0) {
                return(TRUE);
            }
 
            attemptLimit = AddressPoolConnectError(pool, addressId);

            attempts++;
            if (attempts < attemptLimit) {
                continue;
            }
            break;
        }
        XplRWReadLockRelease(&(pool->lock));
        break;
    }
    
    return(FALSE);
}
/* A non-zero errorThreshold will cause addresses in the pool */
/* to be removed if more than that number of errors occur */
/* A non-zero errorTimeThreshold will cause the number of errors */
/* to be reset to zero if that number of seconds pass between consecutive errors */
void
ConnAddressPoolStartup(AddressPool *pool, unsigned long errorThreshold, unsigned long errorTimeThreshold)
{
    pool->address = NULL;
    pool->addressCount = 0;
    pool->weightTable.elements = NULL;
    pool->weightTable.count = 0;
    pool->weightTable.index = 0;
    pool->version = 0;
    pool->nextId = 0;
    pool->errorThreshold = errorThreshold;
    pool->errorTimeThreshold = errorTimeThreshold;
    pool->initialized = ADDRESS_POOL_INITIALIZED_VALUE;
    XplMutexInit(pool->weightTable.mutex);
    XplRWLockInit(&(pool->lock));
}

void
ConnAddressPoolShutdown(AddressPool *pool)
{
    if (pool->initialized == ADDRESS_POOL_INITIALIZED_VALUE) {
        if (pool->address) {
            MemFree(pool->address);
            pool->address = NULL;
            pool->addressCount = 0;
        }
        if (pool->weightTable.elements) {
            MemFree(pool->weightTable.elements);
            pool->weightTable.elements = NULL;
            pool->weightTable.count = 0;
        }

        XplMutexDestroy(pool->weightTable.mutex);
        XplRWLockDestroy(&(pool->lock));
        memset(pool, 0, sizeof(AddressPool));
    }
}

BOOL 
ConnAddressPoolAddHost(AddressPool *pool, char *hostName, unsigned short port, unsigned long weight)
{
    struct hostent *newHost;
    /* validate weight */
    if (weight == 0) {
        return(FALSE);
    } /*TODO, check for overly large weights as well */

    /* validate ip */
    newHost = gethostbyname(hostName);
    if (!newHost) {
        return(FALSE);
    }    
    
    return(AddressPoolAddressAdd(pool, *(uint32_t *)(newHost->h_addr_list[0]), htons(port), weight));
}

BOOL 
ConnAddressPoolAddSockAddr(AddressPool *pool, struct sockaddr_in *addr, unsigned long weight)
{
    return(AddressPoolAddressAdd(pool, addr->sin_addr.s_addr, addr->sin_port, weight));
}

BOOL
ConnAddressPoolRemoveHost(AddressPool *pool, char *hostName, unsigned short port)
{
    struct hostent *newHost;
    long ipNum;

    newHost = gethostbyname(hostName);
    if (!newHost) {
        return(FALSE);
    }    

    XplRWWriteLockAcquire(&(pool->lock));
    ipNum = AddressPoolFind(pool, *(uint32_t *)(newHost->h_addr_list[0]), htons(port));
    if (ipNum == -1) {
        XplRWWriteLockRelease(&(pool->lock));
        return(FALSE);
    }

    AddressPoolRemoveByIndex(pool, ipNum);
    AddressPoolWeightTableUpdate(pool);
    pool->version++;
    XplRWWriteLockRelease(&(pool->lock));
    return(TRUE);
}

BOOL
ConnAddressPoolRemoveSockAddr(AddressPool *pool, struct sockaddr_in *addr)
{
    long ipNum;

    XplRWWriteLockAcquire(&(pool->lock));
    ipNum = AddressPoolFind(pool, addr->sin_addr.s_addr, addr->sin_port);
    if (ipNum == -1) {
        XplRWWriteLockRelease(&(pool->lock));
        return(FALSE);
    }

    AddressPoolRemoveByIndex(pool, ipNum);
    AddressPoolWeightTableUpdate(pool);
    pool->version++;
    XplRWWriteLockRelease(&(pool->lock));
    return(TRUE);
}

/* When an address takes longer than <timeOut> milliseconds to respond, */
/* an error is recorded and the next address in the list is tried */
/* ConnAddressPoolConnet() will only return NULL if all addresses in the pool fail to respond */
Connection *
ConnAddressPoolConnect(AddressPool *pool, unsigned long timeOut)
{
    Connection *conn;

    conn = ConnAlloc(TRUE);
    if (conn) {
        if (AddressPoolConnect(pool, conn, timeOut)) {
            return(conn);
        }
        ConnFree(conn);
    }

    return(NULL);
}

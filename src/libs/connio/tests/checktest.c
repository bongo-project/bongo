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

#include <include/bongocheck.h>
#include "../connio.c"
#ifdef BONGO_HAVE_CHECK

BOOL Exiting = FALSE;
int ServerInit (void *param);

int 
ServerInit (void *param)
{
    unsigned short port = *(unsigned short *)param;
    Connection *serverConn;
    Connection *conn;
#if defined(ADDRESS_POOL_VERBOSE)
    XplConsolePrintf("ServerInit[%d]: Starting\n", port);
#endif
    serverConn = ConnAlloc(FALSE);
    if (!serverConn) {
	XplConsolePrintf("ServerInit[%d]: Could not allocate connection.\n", (int)port);
	return -1;
    }
    
    serverConn->socketAddress.sin_family = AF_INET;
    serverConn->socketAddress.sin_port = htons(port);
    serverConn->socketAddress.sin_addr.s_addr = inet_addr("127.0.0.1");
	
    serverConn->socket = ConnServerSocket(serverConn, 2048);
    if (serverConn->socket == -1) {
        int ret = serverConn->socket;
        XplConsolePrintf("ServerInit[%d]: Could not bind to port %d\n", port, port);
        ConnFree(serverConn);
        return ret;
    }

    while(!Exiting) {
        if (ConnAccept(serverConn, &conn) != -1) {
#if defined(ADDRESS_POOL_VERBOSE)
            XplConsolePrintf("Connection to port %d accepted\n", port);
#endif
            continue;
        }
        XplConsolePrintf("ServerInit[%d]: Experienced a connect error\n", port);
    }

    XplConsolePrintf("ServerInit[%d]: Exiting\n", port);
    return 0;
}

static void
DumpPoolInfo(AddressPool *pool, char *status)
{
#if defined(ADDRESS_POOL_VERBOSE)
    unsigned long i;

    XplConsolePrintf("\n***************** Dumping Pool Info *****************\nEvent: %s\nVersion: %lu\n", status, pool->version);

    XplConsolePrintf("Address Count: %lu\n", pool->addressCount);
    for (i = 0; i < pool->addressCount; i++) {
        XplConsolePrintf("  %lu:  Address: %s Port: %d Weight: %lu Errors: %lu\n", i, inet_ntoa(pool->address[i].addr.sin_addr), (int)ntohs(pool->address[i].addr.sin_port), pool->address[i].weight, (unsigned long)XplSafeRead(pool->address[i].errorCount));
    }
   
    if (pool->weightTable.elements) {
        XplConsolePrintf("Weight Table Size: %lu\nWeight Table Index: %lu\n", pool->weightTable.count, pool->weightTable.index);
        for (i = 0; i < pool->weightTable.count; i++) {
            XplConsolePrintf("  %lu: %lu\n", i, pool->weightTable.elements[i]);
        }
    } else {
        XplConsolePrintf("Weight Table Size: nil\n");
    }

    XplConsolePrintf("*****************************************************\n\n");
#endif
}

static BOOL
ValidateWeightTable(AddressPool *pool)
{
    long *expected;
    unsigned long i;

    if (pool->addressCount == 0) {
        if (pool->address == NULL) {
            if (pool->weightTable.elements == NULL) {
                return(TRUE);
            }
        }
        return(FALSE);
    }

    if (pool->addressCount == 1) {
        if (pool->address != NULL) {
            if (pool->weightTable.elements == NULL) {
                return(TRUE);
            }
        }
        return(FALSE);        
    }

    expected = MemMalloc(sizeof(long) * pool->addressCount); 
    for (i = 0; i < pool->addressCount; i++) {
        expected[i] = (long)pool->address[i].weight;
    }

    for (i = 0; i < pool->weightTable.count; i++) {
        expected[pool->weightTable.elements[i]]--;
    }

    for (i = 0; i < pool->addressCount; i++) {
        if (expected[i] != 0) {
            return(FALSE);
        }
    }

    return(TRUE);
}

typedef struct {
    unsigned long id;
    unsigned long misses;
    unsigned long hits;
    BOOL removed;
} AddressReportAddress;

typedef struct {
    AddressReportAddress *address;
    unsigned long addressCountBegin;
    unsigned long addressCountEnd;
    unsigned long addressCountUnique;
    unsigned long apiFailures;
    unsigned long totalMisses;
} AddressReportPool;

static BOOL
AddressReportAddNewAddress(AddressReportPool *report, unsigned long id)
{
    AddressReportAddress *tmpAddressList;

    tmpAddressList = MemRealloc(report->address, sizeof(AddressReportAddress) * (report->addressCountUnique + 1));
    if (tmpAddressList) {
        report->address = tmpAddressList;
        report->address[report->addressCountUnique].id = id;
        report->address[report->addressCountUnique].misses = 0;
        report->address[report->addressCountUnique].hits = 0;
        report->address[report->addressCountUnique].removed = FALSE;
        report->addressCountUnique++;
        return(TRUE);
    }
    return(FALSE);
}


static BOOL
AddressReportAddNewAddresses(AddressPool *pool, AddressReportPool *report)
{
    unsigned long i;
    unsigned long j;

    for (i = 0; i < pool->addressCount; i++) {
        BOOL found = FALSE;
        for (j = 0; j < report->addressCountUnique; j++) {
            if (pool->address[i].id == report->address[j].id) {
                found = TRUE;
                break;
            }
        }
        if (!found) {
            if (AddressReportAddNewAddress(report, pool->address[i].id)) {
                continue;
            }
            return(FALSE);
        }
    } 
    return(TRUE);
}

static unsigned long
AddressReportExamineAddresses(AddressPool *pool, AddressReportPool *report)
{
    unsigned long i;
    unsigned long j;
    unsigned long misses = 0;

    for (j = 0; j < report->addressCountUnique; j++) {
        BOOL found = FALSE;
        for (i = 0; i < pool->addressCount; i++) {
            if (report->address[j].id == pool->address[i].id) {
                report->address[j].misses = (unsigned long)XplSafeRead(pool->address[i].errorCount);
                misses += report->address[j].misses;
                found = TRUE;
                break;
            }
        }
        if (!found) {
            report->address[j].removed = TRUE;
            report->address[j].misses = pool->errorThreshold;
            misses += report->address[j].misses;
        }
    }
    return(misses);
}

static long
AddressReportFindAddress(AddressReportPool *report, unsigned long addrId)
{
    unsigned long j;

    for (j = 0; j < report->addressCountUnique; j++) {
        if (report->address[j].id == addrId) {
            return((long)j);
        }
    }
    return(-1);
}

static long
AddressReportFindAddressId(AddressPool *pool, unsigned short port)
{
    unsigned long j;

    for (j = 0; j < pool->addressCount; j++) {
        if (port == pool->address[j].addr.sin_port) {
            return(pool->address[j].id);
        }
    }
    return(-1);
}


static BOOL
AddressReportIncrementHitCount(AddressReportPool *report, unsigned long addrId)
{
    long sequence;

    sequence = AddressReportFindAddress(report, addrId);
    if (sequence != -1) {
        report->address[sequence].hits++;
        return(TRUE);
    }

    if (AddressReportAddNewAddress(report, addrId)) {
        sequence = AddressReportFindAddress(report, addrId);
        if (sequence != -1) {
            report->address[sequence].hits++;
            return(TRUE);
        }
    }

    return(FALSE);
}

static BOOL
AddressReportGenerate(AddressPool *pool, unsigned long requestCount, AddressReportPool *report)
{
    unsigned long i;
    Connection *conn;
    BOOL logicFailure = FALSE;
    long id;

    memset(report, 0, sizeof(AddressReportPool));
    report->addressCountBegin = pool->addressCount;

    /* Add initial addresses to the report */
    
    for (i = 0; i < requestCount; i++) {
        if(AddressReportAddNewAddresses(pool, report)) {
            conn = ConnAddressPoolConnect(pool, 20);
            if (conn) {
                if ((id = AddressReportFindAddressId(pool, conn->socketAddress.sin_port)) != -1) {
                    if (AddressReportIncrementHitCount(report, id)) {
                        ConnClose(conn, 0);
                        ConnFree(conn);
                        continue;
                    }
                }
                logicFailure = TRUE;
                ConnClose(conn, 0);
                ConnFree(conn);
                break;
            }
            report->apiFailures++;
            continue;
        }
        logicFailure = TRUE;
        break;
    }
   
    report->totalMisses = AddressReportExamineAddresses(pool, report);

    report->addressCountEnd = pool->addressCount;
    
    return(!logicFailure);
}

START_TEST(addrpool_empty)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 0, 0);
    DumpPoolInfo(&addrPool, "after PoolStartup");
    fail_unless(AddressReportGenerate(&addrPool, 7, &report));
    fail_unless(report.apiFailures == 7);
    fail_unless(report.totalMisses == 0);
    fail_unless(report.addressCountBegin == 0);
    fail_unless(report.addressCountEnd == 0);
    fail_unless(report.addressCountUnique == 0);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

START_TEST(test0)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 0, 0);
    DumpPoolInfo(&addrPool, "after PoolStartup");
    fail_unless(AddressReportGenerate(&addrPool, 7, &report));
    fail_unless(report.apiFailures == 7);
    fail_unless(report.totalMisses == 0);
    fail_unless(report.addressCountBegin == 0);
    fail_unless(report.addressCountEnd == 0);
    fail_unless(report.addressCountUnique == 0);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

START_TEST(test1)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(1);
    XplDelay(1);
    XplDelay(1);

    ConnAddressPoolStartup(&addrPool, 0, 0);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5002, 1);
    DumpPoolInfo(&addrPool, "after adding 5002");
    fail_unless(addrPool.addressCount == 1);
    fail_unless(ValidateWeightTable(&addrPool));
    fail_unless(AddressReportGenerate(&addrPool, 7, &report));
    fail_unless(report.apiFailures == 0);
    fail_unless(report.totalMisses == 0);
    fail_unless(report.addressCountBegin == 1);
    fail_unless(report.addressCountEnd == 1);
    fail_unless(report.addressCountUnique == 1);
    fail_unless(report.address[0].hits == 7);
    fail_unless(report.address[0].misses == 0);
    fail_unless(report.address[0].removed == FALSE);
    ConnAddressPoolShutdown(&addrPool);



    ConnShutdown();
    // TODO
    fail_unless(1==1);
    fail_if(1 == 2);
}
END_TEST

START_TEST(test2)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 0, 0);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5002, 1);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5003, 1);
    DumpPoolInfo(&addrPool, "after adding 5003");
    fail_unless(addrPool.addressCount == 2);
    fail_unless(ValidateWeightTable(&addrPool));
    fail_unless(AddressReportGenerate(&addrPool, 8, &report));
    fail_unless(report.apiFailures == 0);
    fail_unless(report.totalMisses == 0);
    fail_unless(report.addressCountBegin == 2);
    fail_unless(report.addressCountEnd == 2);
    fail_unless(report.addressCountUnique == 2);
    fail_unless(report.address[0].hits == 4);
    fail_unless(report.address[0].misses == 0);
    fail_unless(report.address[0].removed == FALSE);
    fail_unless(report.address[1].hits == 4);
    fail_unless(report.address[1].misses == 0);
    fail_unless(report.address[1].removed == FALSE);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

START_TEST(test3)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 0, 0);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5002, 1);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5003, 1);
    ConnAddressPoolAddSockAddr(&addrPool, &addrPool.address[1].addr, 3);
    DumpPoolInfo(&addrPool, "after adding 5003 again as SockAddr");
    fail_unless(addrPool.addressCount == 2);
    fail_unless(ValidateWeightTable(&addrPool));
    fail_unless(AddressReportGenerate(&addrPool, 8, &report));
    fail_unless(report.apiFailures == 0);
    fail_unless(report.totalMisses == 0);
    fail_unless(report.addressCountBegin == 2);
    fail_unless(report.addressCountEnd == 2);
    fail_unless(report.addressCountUnique == 2);
    fail_unless(report.address[0].hits == 2);
    fail_unless(report.address[0].misses == 0);
    fail_unless(report.address[0].removed == FALSE);
    fail_unless(report.address[1].hits == 6);
    fail_unless(report.address[1].misses == 0);
    fail_unless(report.address[1].removed == FALSE);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

START_TEST(test4)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 0, 0);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5002, 1);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5003, 1);
    ConnAddressPoolAddSockAddr(&addrPool, &addrPool.address[1].addr, 3);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5004, 2);
    DumpPoolInfo(&addrPool, "after adding 5004");
    fail_unless(addrPool.addressCount == 3);
    fail_unless(ValidateWeightTable(&addrPool));
    fail_unless(AddressReportGenerate(&addrPool, 24, &report));
    DumpPoolInfo(&addrPool, "post test");
    fail_unless(report.apiFailures == 0);
    fail_unless(report.totalMisses == 12);
    fail_unless(report.addressCountBegin == 3);
    fail_unless(report.addressCountEnd == 3);
    fail_unless(report.addressCountUnique == 3);
    fail_unless(report.address[0].hits == 6);
    fail_unless(report.address[0].misses == 0);
    fail_unless(report.address[0].removed == FALSE);
    fail_unless(report.address[1].hits == 18);
    fail_unless(report.address[1].misses == 0);
    fail_unless(report.address[1].removed == FALSE);
    fail_unless(report.address[2].hits == 0);
    fail_unless(report.address[2].misses == 12);
    fail_unless(report.address[2].removed == FALSE);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

START_TEST(test5)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 4, 600);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5002, 1);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5003, 1);
    ConnAddressPoolAddSockAddr(&addrPool, &addrPool.address[1].addr, 3);
    ConnAddressPoolAddHost(&addrPool, "localhost", 5004, 2);
    ConnAddressPoolRemoveSockAddr(&addrPool, &(addrPool.address[0].addr));
    DumpPoolInfo(&addrPool, "after removing 5002");
    fail_unless(addrPool.addressCount == 2);
    fail_unless(ValidateWeightTable(&addrPool));
    fail_unless(AddressReportGenerate(&addrPool, 24, &report));
    DumpPoolInfo(&addrPool, "post test");
    fail_unless(report.apiFailures == 0);
    fail_unless(report.totalMisses == 4);
    fail_unless(report.addressCountBegin == 2);
    fail_unless(report.addressCountEnd == 1);
    fail_unless(report.addressCountUnique == 2);
    fail_unless(report.address[0].hits == 24);
    fail_unless(report.address[0].misses == 0);
    fail_unless(report.address[0].removed == FALSE);
    fail_unless(report.address[1].hits == 0);
    fail_unless(report.address[1].misses == 4);
    fail_unless(report.address[1].removed == TRUE);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

START_TEST(test6)
{
    long ccode;
    XplThreadID id;
    unsigned long port1 = 5002;
    unsigned long port2 = 5003;
    AddressPool addrPool;
    AddressReportPool report;

    MemoryManagerOpen("CONNIO Test");
    ConnStartup(5, TRUE);
    XplBeginThread(&id, ServerInit, 8192, &port1, ccode);
    XplBeginThread(&id, ServerInit, 8192, &port2, ccode);

    XplDelay(3);

    ConnAddressPoolStartup(&addrPool, 7, 100);
    ConnAddressPoolAddHost(&addrPool, "127.0.01", 5005, 5);
    ConnAddressPoolAddHost(&addrPool, "127.0.01", 5006, 2);
    DumpPoolInfo(&addrPool, "no valid addresses");
    fail_unless(addrPool.addressCount == 2);
    fail_unless(ValidateWeightTable(&addrPool));
    fail_unless(AddressReportGenerate(&addrPool, 3, &report));
    DumpPoolInfo(&addrPool, "post test");
    fail_unless(report.apiFailures == 3);
    fail_unless(report.totalMisses == 13);
    fail_unless(report.addressCountBegin == 2);
    fail_unless(report.addressCountEnd == 1);
    fail_unless(report.addressCountUnique == 2);
    fail_unless(report.address[0].hits == 0);
    fail_unless(report.address[0].misses == 7);
    fail_unless(report.address[0].removed == TRUE);
    fail_unless(report.address[1].hits == 0);
    fail_unless(report.address[1].misses == 6);
    fail_unless(report.address[1].removed == FALSE);
    ConnAddressPoolShutdown(&addrPool);

    ConnShutdown();
    MemoryManagerClose("CONNIO Test");
}
END_TEST

//TODO write your tests above, and/or
// pound include other tests of your own here
START_CHECK_SUITE_SETUP("Connio library")
    CREATE_CHECK_CASE   (tc_core  , "Core"  );
    CHECK_SUITE_ADD_CASE(top_suite, tc_core );
    CHECK_CASE_ADD_TEST (tc_core  , addrpool_empty   );
    CHECK_CASE_ADD_TEST (tc_core  , test0   );
    CHECK_CASE_ADD_TEST (tc_core  , test1   );
    CHECK_CASE_ADD_TEST (tc_core  , test2   );
    CHECK_CASE_ADD_TEST (tc_core  , test3   );
    CHECK_CASE_ADD_TEST (tc_core  , test4   );
    CHECK_CASE_ADD_TEST (tc_core  , test5   );
    CHECK_CASE_ADD_TEST (tc_core  , test6   );
END_CHECK_SUITE_SETUP
#else
SKIP_CHECK_TESTS
#endif


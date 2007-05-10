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
#include <connio.h>
#include <xplresolve.h>

#ifdef HAVE_RESOLV_H
#include <resolv.h>
#include <sys/types.h>
#include <netinet/in.h>
#include <arpa/nameser.h>
#endif


#ifndef HAVE_RESOLV_H
# define USE_INTERNAL_RESOLVER
#endif

#define MTU 1024

#define HEADERSIZE    sizeof(ResolveHeader)
#define RECINFOSIZE   sizeof(ResolveRecordInfo)
#define MXDATASIZE    sizeof(ResolveMxData)
#define ADATASIZE     sizeof(ResolveAData)
#define MAXTRIES      10
#define MAXCNAMELOOPS 10
#define TIMEOUT       5    /* Seconds */

#define DEBUG_RESOLVER  0

#if DEBUG_RESOLVER
# define d XplConsolePrintf
#else
# define d if(0) printf
#endif

#if !HAVE_DECL_NS_C_IN
# define ns_c_in C_IN
#endif


#ifdef USE_INTERNAL_RESOLVER
static struct
{
    char **resolvers;
    int resolverCount;
}  Resolver = { 0 };
#endif

#if defined(WIN32) || defined(NETWARE) || defined(LIBC)
#pragma pack (push, 1)
#endif

/* Pieces of a DNS packet */
typedef struct _ResolveHeader {
    unsigned short id;          /* Query ID -- hopefully unique                   */
    unsigned short flags;       /* Bit 0: query or response
                                 * Bit 1-4: opcode
                                 * Bit 5: authoritative answer
                                 * Bit 6: truncation
                                 * Bit 7: recursion desired
                                 * Bit 8: recursion available
                                 * Bit 9-11: unused, must be 0
                                 * Bit 12-15: response code                       */
    unsigned short qdcount;     /* Number of questions                            */
    unsigned short ancount;     /* Number of answers                              */
    unsigned short nscount;     /* Number of name server RRs in authority section */
    unsigned short arcount;     /* Number of RRs in additional records section    */
} ResolveHeader;

struct _ResolveQuestionInfo {
    unsigned short type;        /* Type of question        */
    unsigned short class;    /* Class of question        */
} Xpl8BitPackedStructure;

typedef struct _ResolveQuestionInfo ResolveQuestionInfo;

struct _ResolveRecordInfo {
    unsigned short type;
    unsigned short class;
    unsigned int ttl;
    unsigned short datalength;
} Xpl8BitPackedStructure;

typedef struct _ResolveRecordInfo ResolveRecordInfo;

struct _ResolveMxData {
    unsigned short preference;
} Xpl8BitPackedStructure;

typedef struct _ResolveMxData ResolveMxData;

struct _ResolveAData {
    unsigned int address;
} Xpl8BitPackedStructure;

typedef struct _ResolveAData ResolveAData;

#if defined(WIN32) || defined(NETWARE) || defined(LIBC)
#pragma pack (pop)
#endif

/* Constants for flags field of ResolveHeader */
#define F_TYPERESPONSE      0x8000    /* Packet contains response                       */
#define F_TYPEQUERY         0x0000    /* Packet contains query                          */

#define F_OPSTATUS          0x1000    /* Server status query                            */
#define F_OPINVERSE         0x0800    /* Inverse query                                  */
#define F_OPSTANDARD        0x0000    /* Standard query                                 */

#define F_AUTHORITATIVE     0x0400    /* Response is authoritative                      */
#define F_TRUNCATED         0x0200    /* Packet was truncated by network                */
#define F_WANTRECURSIVE     0x0100    /* Recursive lookup requested                     */
#define F_RECURSIVEUSED     0x0080    /* Recursive lookup available/used                */

#define F_RCMASK            0x000F    /* Throw away all but the return code             */
#define F_ERRREFUSED        0x0005    /* The request was refused                        */
#define F_ERRNOTIMP         0x0004    /* Query type isn't implemented by server         */
#define F_ERRNAME           0x0003    /* The name doesn't exist                         */
#define F_ERRFAILURE        0x0002    /* The name server experience an internal error   */
#define F_ERRFORMAT         0x0001    /* The server can't interpret the query           */
#define F_ERRNONE           0x0000    /* No errors occurred                             */

/* Query class constants */
#define CLASS_IN            0x0001    /* Internet class */

/* Prototypes */
static int DecodeName(char *response, unsigned long offset, char *name);
static int CompareHosts(const void *name1, const void *name2);
static int ResolveName (char *host, int type, char *answer, int answerLen);

EXPORT int 
XplDnsResolve(char *host, XplDnsRecord **list, int type)
{
    BOOL cnameLooping;
    char answer[MTU];
    char hostName[MAXEMAILNAMESIZE + 1];
    char hostCopy[MAXEMAILNAMESIZE + 1];
    ResolveHeader *answerH;
    XplDnsRecord *answers;
    ResolveMxData *mxData;
    ResolveAData *aData;
    struct PTRData *ptrData;
    ResolveRecordInfo *recInfo;
    unsigned long answerOff;
    unsigned long i;
    unsigned long cnameLoops = 0;
    unsigned long numAnswers;
    int size;

#if DEBUG_RESOLVER
#ifdef NETWARE
    NETINET_DEFINE_CONTEXT                                        /* To make the netware happy                */
#endif
#endif
    /* Setup */
    if (type != XPL_RR_PTR) {
        strcpy(hostCopy, host);
    } else {
        int a,b,c,d1, ok;
        char *ptr, *ptr2;

        if (host[strlen(host) - 1] != 'A') {
            ok = 0;
            strcpy(hostCopy, host);
            ptr=strchr(hostCopy, '.');
            if (ptr) {
                *ptr = '\0';
                a = atol(hostCopy);
                ptr2 = strchr(ptr + 1, '.');
                if (ptr2) {
                    *ptr2 = '\0';
                    b = atol(ptr + 1);
                    ptr = strchr(ptr2 + 1, '.');
                    if (ptr) {
                        *ptr = '\0';
                        c = atol(ptr2+1);
                        d1 = atol(ptr+1);
                        sprintf(hostCopy, "%d.%d.%d.%d.IN-ADDR.ARPA", d1, c, b, a);
                        ok = 1;
                    }
                }
            }
            if (!ok) {
                return(XPL_DNS_BADHOSTNAME);
            }
        } else {
            strcpy(hostCopy, host);
        }
    }

    srand(time(NULL));

    answerH = (ResolveHeader*)&answer;    /* easier to write and more readable */

    *list = NULL;                         /* Just to be safe */

    /* We loop here in case we get a CNAME in response to the query. We'll stop looping when
     * we get an MX or A record, which is what we're looking for */
    do {
	int len = 0;
        d("Starting query loop\n");

        cnameLooping = FALSE;    /* Will be set to TRUE if we get a CNAME response */

	d("hostname: %s\n", hostCopy);
	size = ResolveName(hostCopy, type, answer, sizeof(answer));

	if (size < 0) {
		return(size);
	}
	
#if DEBUG_RESOLVER
        XplConsolePrintf("---------- RESPONSE ----------\n");
#if 0
        for(x = 0; x < size; ++x) {
            XplConsolePrintf("%02x %c ", answer[x], answer[x]);
        }
#endif
        XplConsolePrintf("\n------------------------------\n");
#endif

        /* Decode packet */
        answerH->flags = ntohs(answerH->flags);
        if ((answerH->flags & F_RCMASK) != F_ERRNONE) {
            if ((answerH->flags & F_RCMASK) == F_ERRNAME) {
                return(XPL_DNS_BADHOSTNAME);
            } else {
                return(XPL_DNS_FAIL);
            }
        }
    
        numAnswers = ntohs(answerH->ancount);
        if (numAnswers == 0) {
            return(XPL_DNS_NORECORDS);
        }

        /* The extra record is for a "NULL" termination record */
        answers = MemMalloc(sizeof(XplDnsRecord) * (numAnswers + 1));
        if (answers == NULL) {
            return(XPL_DNS_FAIL);
        }

        answerOff = HEADERSIZE;
        answerOff += DecodeName(answer, answerOff, answers[0].A.name);
        answerOff += sizeof(ResolveQuestionInfo);

        /* answerOff = queryoff; */    /* Skip question, which includes modified header */
        for(i = 0; (i < numAnswers) && (!cnameLooping); ++i) {
            answerOff += DecodeName(answer, answerOff, answers[i].A.name);
	    recInfo = (ResolveRecordInfo*)(answer + answerOff);
            answerOff += RECINFOSIZE;

            d("Question  : %s\n", answers[i].A.name);

            recInfo->type = ntohs(recInfo->type);
            switch(recInfo->type) {
	    case XPL_RR_MX:
		mxData = (ResolveMxData*)(answer + answerOff);
		answers[i].MX.preference = ntohs(mxData->preference);
		answerOff += MXDATASIZE;
		answerOff += DecodeName(answer, answerOff, answers[i].MX.name);
		answers[i].MX.addr.s_addr = 0;

		d("MX Record\n");
		d("Preference: %d\n", ntohs(answers[i].MX.preference));
		d("Name      : %s\n", answers[i].MX.name);
		break;
                
	    case XPL_RR_A:
		aData = (ResolveAData*)(answer + answerOff);
		answers[i].A.addr.s_addr = aData->address;
		answerOff += ADATASIZE;
		
		d("A Record\n");
		d("Address   : %s\n", inet_ntoa(answers[i].A.addr));
		break;

	    case XPL_RR_CNAME:
		/* Instead of giving the CNAME back to the caller, we'll be nice and do the
		 * A lookup for them. */
		answerOff += DecodeName(answer, answerOff, answers[i].A.name);
		d("Got CNAME, looking up %s\n", answers[i].A.name);
		strcpy(hostCopy, answers[i].A.name);
		MemFree(answers);
		answers = NULL;

		cnameLooping = TRUE;
		++cnameLoops;
		break;

            case XPL_RR_TXT:
		len = (int) answer[answerOff];
                strncpy(answers[i].TXT.txt, answer + answerOff + 1, len);
                d("TXT Record\n");
                d("Answer   : %s\n", answers[i].TXT.txt);
                break;

	    case XPL_RR_PTR: {
		ptrData = (struct PTRData*)(answer + answerOff);
		answerOff += DecodeName(answer, answerOff, answers[i].PTR.name);
		d("Got PTR, name:%s\n", answers[i].PTR.name);
		break;
	    }

	    case XPL_RR_SOA: {
		unsigned char    *ptr;

		answerOff += DecodeName(answer, answerOff, answers[i].SOA.mname);
		answerOff += DecodeName(answer, answerOff, answers[i].SOA.rname);

		answers[i].SOA.serial = ntohl(answer[answerOff] | answer[answerOff + 1] << 8 | answer[answerOff + 2] << 16 | answer[answerOff + 3] << 24);
		answerOff += 4;

		answers[i].SOA.refresh = ntohl(answer[answerOff] | answer[answerOff + 1] << 8 | answer[answerOff + 2] << 16 | answer[answerOff + 3] << 24);
		answerOff += 4;

		answers[i].SOA.retry=ntohl(answer[answerOff] | answer[answerOff + 1] << 8 | answer[answerOff + 2] << 16 | answer[answerOff + 3] << 24);
		answerOff += 4;

		answers[i].SOA.expire=ntohl(answer[answerOff] | answer[answerOff + 1] << 8 | answer[answerOff + 2] << 16 | answer[answerOff + 3] << 24);
		answerOff += 4;

		answers[i].SOA.minimum=ntohl(answer[answerOff] | answer[answerOff + 1] << 8 | answer[answerOff + 2] << 16 | answer[answerOff + 3] << 24);
		answerOff += 4;

		/* Now fixup the email address */
		ptr = answers[i].SOA.rname;
		while (ptr[0] && ptr[0] != '.') {
		    ptr++;
		}

		if (ptr[0] == '.') {
		    ptr[0] = '@';
		}

		d("SOA Record\n");
		d("MName     : %s\n", answers[i].SOA.mname);
		d("RName     : %s\n", answers[i].SOA.rname);
		d("Serial    : %lu\n", answers[i].SOA.serial);
		d("Refresh   : %lu\n", answers[i].SOA.refresh);
		d("Retry     : %lu\n", answers[i].SOA.retry);
		d("Expire    : %lu\n", answers[i].SOA.expire);
		d("Minimum   : %lu\n", answers[i].SOA.minimum);

		break;
	    }

	    default:        /* We don't yet know how to deal with any other kinds of records */
		--i;
		--numAnswers;
		break;
            }
        }
    } while (cnameLooping && (cnameLoops < MAXCNAMELOOPS));

    if (cnameLoops == MAXCNAMELOOPS) {
        if (answers) {
            MemFree(answers);
        }
        return(XPL_DNS_FAIL);
    }

    /* We have to sort the MX records by preference. If we are one of the MX hosts, we also
     * have to remove ourselves and anyone with higher precedence than us to prevent mail
     * loops.
     */
    if (type == XPL_RR_MX) {
        gethostname(hostName, sizeof(hostName));
        if (numAnswers > 1) {
            qsort(answers, numAnswers, sizeof(XplDnsRecord), CompareHosts);
        }

        for (i = 0; i < numAnswers; ++i) {
            if (XplStrCaseCmp(hostName, answers[i].A.name) == 0) {
                unsigned long match;

                /* Remove all hosts with our preference to prevent possible loops */
                match = i;
                while ((i > 0) && (answers[i - 1].MX.preference == answers[match].MX.preference)) {
                    i--;
                }
                numAnswers = i;
                break;
            }
        }
    }
    
    if (numAnswers == 0) {
        MemFree(answers);
        return(XPL_DNS_NORECORDS);
    }

    answers[numAnswers].A.name[0] = '\0';
    answers[numAnswers].MX.preference = 0;

    *list = answers;

#if DEBUG_RESOLVER
    if (type == XPL_RR_A) {
        i = 0;
        while ((*list)[i].A.name[0] != '\0') {
            XplConsolePrintf("%15s  : %s\n", inet_ntoa((*list)[i].A.addr), (*list)[i].A.name);
            ++i;
        }
    }
#endif

    return(XPL_DNS_SUCCESS);
}

static int
CompareHosts(const void *name1, const void *name2) 
{
    if (((XplDnsRecord*)name1)->MX.preference > ((XplDnsRecord*)name2)->MX.preference) {
        return(1);
    } else if (((XplDnsRecord*)name1)->MX.preference < ((XplDnsRecord*)name2)->MX.preference) {
        return(-1);
    }
    
    return(0);
}

static int
DecodeName(char *response, unsigned long offset, char *name) 
{
    BOOL pointer = FALSE;
    unsigned char length;
    unsigned short nameOffset = 0;
    unsigned long encodedLen = 0;

    /* Process one label at a time till we get to 0 length label */

    do {
        /* Check for a pointer */
        if ((response[offset] & 0xC0) == 0xC0) {
            unsigned short target;
            
            target = (response[offset++] & 0x3F) * 256;
            target += (unsigned char)response[offset];
            
            offset = target;

            /* The length of the encoded name only increases if we haven't already
             * followed a pointer */
            if (!pointer) {
                encodedLen += 2;
            }
            
            pointer = TRUE;
        }
        
        length = response[offset++];

        if (length) {
            memcpy(name + nameOffset, response + offset, length);
            nameOffset += length;
            name[nameOffset++] = '.';
            
            offset += length;
        } else {
            /* Handle situation where the first length byte is 0 (=[ROOT]) */
            if (!pointer && encodedLen == 0) {
                name[0] = '.';
                name[1] = '\0';
                return(1);
            }
        }

        if (!pointer) {
            encodedLen += length + 1;
        }
    } while (response[offset]!=0);

    if (!pointer) {
        ++encodedLen;
    }

    /* Yes, we do want to overwrite the final '.' */
    name[--nameOffset] = '\0';
    
    return(encodedLen);
}

#ifdef USE_INTERNAL_RESOLVER

static int 
EncodeName(char *name, char *buf) 
{
    BOOL done = FALSE;
    char *ptr1 = name;
    char *ptr2 = NULL;
    unsigned char length = 0;
    unsigned char index = 0;
    
    /* Encode one label at a time */
    do {
        /* Find the end of the label */
        ptr2 = strchr(ptr1, '.');

        /* Is this the last label? */
        if (ptr2 == NULL) {
            ptr2 = ptr1 + strlen(ptr1) + 1;    /* Should already be '\0' */
            done = TRUE;
        }

        /* Separate this label */
        *ptr2 = '\0';
        
        /* Put in length byte */
        length = (unsigned char)strlen(ptr1);
        buf[index++] = length;

        /* Copy label */
        strcpy(buf + index, ptr1);
        index += length;

        /* Set up for the next loop */
        if (!done)
            *ptr2 = '.';

        ptr1 = ptr2 + 1;
    } while (!done);
    
    /* Terminate with 0 length byte */
    buf[index++] = 0;
    
    return(index);        /* Tell caller how long the encoded name is */
}

static int
ResolveName (char *host, int type, char *answer, int answerLen)
{
    int sock;
    unsigned char tries;
    ResolveHeader *queryH;
    ResolveHeader *answerH;
    ResolveQuestionInfo *queryQuestion;
    struct sockaddr_in toSin;
    struct sockaddr_in fromSin;
    unsigned long queryOff;
    int actualTries;
    int curResolver;
    char query[MTU];
    int size;

    if (Resolver.resolverCount < 1) {
        return(XPL_DNS_FAIL);
    }

    if ((strlen(host) > MAXEMAILNAMESIZE) || (!strchr(host, '.'))) {
        return(XPL_DNS_BADHOSTNAME);
    }

    curResolver = 0;

    queryH = (ResolveHeader*)query;      /* These are overlays to make the code */
    answerH = (ResolveHeader*)answer;    /* easier to write and more readable */

    sock = socket(PF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (sock < 0) {
	return(XPL_DNS_FAIL);
    }
    
    memset(&toSin, 0, sizeof(toSin));
    toSin.sin_family = AF_INET;
    /* Address set in the loop below */
    toSin.sin_port = htons(53);
    
    memset(&fromSin, 0, sizeof(fromSin));
    fromSin.sin_family = AF_INET;
    fromSin.sin_port = htons(0);

    if (bind(sock, (struct sockaddr*)&fromSin, sizeof(fromSin))) {
	    IPclose(sock);
	    return(XPL_DNS_FAIL);
    }

    /* Initialize */
    tries = 0;
    actualTries = Resolver.resolverCount*MAXTRIES;

    /* Build query */
    d("Building query\n");

    queryH->id = htons((unsigned short)rand());
    queryH->flags = htons(F_TYPEQUERY | F_OPSTANDARD | F_WANTRECURSIVE);
    queryH->qdcount = htons(1);
    queryH->ancount = queryH->nscount = queryH->arcount = htons(0);

    queryOff = HEADERSIZE;
    queryOff += EncodeName(host, query + queryOff);
    queryQuestion = (ResolveQuestionInfo*)(query + queryOff);
    queryQuestion->type = htons((unsigned short)type);
    queryQuestion->class = htons(CLASS_IN);
    queryOff += sizeof(queryQuestion->type) + sizeof(queryQuestion->class);
    
#if DEBUG_RESOLVER
    { unsigned long x;
    XplConsolePrintf("---------- QUERY ----------\n");
#if 0
    for(x = 0; x < queryOff; ++x) {
	XplConsolePrintf("%02x %c ", query[x], query[x]);
    }
#endif
    d("hostname: %s\n", host);
    
    XplConsolePrintf("\n---------------------------\n");
    }
    
#endif
	
    /* Run query */
    do {
	toSin.sin_addr.s_addr = inet_addr(Resolver.resolvers[curResolver]);

	if (sendto(sock, query, queryOff, 0, (struct sockaddr*)&toSin, sizeof(toSin)) != (int)queryOff) {
	    IPclose(sock);
	    return(XPL_DNS_FAIL);
	}

	size = XplIPRead((void *)sock, answer, answerLen, TIMEOUT);
	if (size < 0) {
	    curResolver++;
	    if (curResolver >= Resolver.resolverCount)
		curResolver = 0;
	    /* Timed out */
	    continue;
	}

	d("Size = %d\n", size);

	if (size > 0 && answerH->id == queryH->id) {
	    break;
	}

	curResolver++;
	if (curResolver >= Resolver.resolverCount) {
	    curResolver = 0;
	}
    } while (++tries < actualTries);
    IPclose(sock);
	
    if (tries >= actualTries) {
	return(XPL_DNS_TIMEOUT);
    }
    return size;
}

EXPORT void
XplDnsAddResolver(const char *resolverValue)
{
    Resolver.resolvers = MemRealloc(Resolver.resolvers, (Resolver.resolverCount+1) * sizeof(char *));
    if (!Resolver.resolvers) {
        XplConsolePrintf("Could not add resolver, out of memory.");
        return;
    }

    Resolver.resolvers[Resolver.resolverCount] = MemStrdup(resolverValue);
    if (!Resolver.resolvers[Resolver.resolverCount]) {
        XplConsolePrintf("Could not add resolver, out of memory.");
        return;
    }

    Resolver.resolverCount++;
}

static void
FreeResolvers(void)
{
    int i;

    for (i = 0; i < Resolver.resolverCount; i++)
        MemFree(Resolver.resolvers[i]);

    if (Resolver.resolvers) {
        MemFree(Resolver.resolvers);
    }
}


BOOL
XplResolveStop(void)
{
    FreeResolvers();
    return(TRUE);
}

BOOL
XplResolveStart(void)
{
    return TRUE;
}

#elif defined (HAVE_RESOLV_H)

EXPORT void
XplDnsAddResolver(const char *resolverValue)
{
    /* FIXME: Ignored, maybe we should munge _res ? */
}

static int
ResolveName (char *host, int type, char *answer, int answerLen) 
{
    int size;
    
    h_errno = 0;
    
    size = res_search(host, ns_c_in, type, answer, answerLen);
    
    if (size >= 0) {
	return size;
    } else {
	switch (h_errno) {
	case NO_ADDRESS:
	    return XPL_DNS_NORECORDS;
	case HOST_NOT_FOUND :
	case NO_RECOVERY:
	case TRY_AGAIN:
	default :
	    return XPL_DNS_FAIL;
	}
    }
}

BOOL
XplResolveStop(void)
{
    return(TRUE);
}

BOOL
XplResolveStart(void)
{
    if (res_init() == 0) {
	return TRUE;
    } else {
	return FALSE;
    }
}

#endif

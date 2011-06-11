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

#include <resolv.h>
#include <sys/types.h>
#include <netinet/in.h>
#include <arpa/nameser.h>

#define MTU 1024

#define HEADERSIZE    sizeof(ResolveHeader)
#define RECINFOSIZE   sizeof(ResolveRecordInfo)
#define MXDATASIZE    sizeof(ResolveMxData)
#define ADATASIZE     sizeof(ResolveAData)
#define MAXTRIES      10
#define MAXCNAMELOOPS 10
#define TIMEOUT       5    /* Seconds */

#if !HAVE_DECL_NS_C_IN
# define ns_c_in C_IN
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
    //struct PTRData *ptrData;
    ResolveRecordInfo *recInfo;
    unsigned long answerOff;
    unsigned long i;
    unsigned long cnameLoops = 0;
    unsigned long numAnswers;
    int size;

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
 
        cnameLooping = FALSE;    /* Will be set to TRUE if we get a CNAME response */

	size = ResolveName(hostCopy, type, answer, sizeof(answer));

	if (size < 0) {
		return(size);
	}
	
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

            recInfo->type = ntohs(recInfo->type);
            switch(recInfo->type) {
	    case XPL_RR_MX:
		mxData = (ResolveMxData*)(answer + answerOff);
		answers[i].MX.preference = ntohs(mxData->preference);
		answerOff += MXDATASIZE;
		answerOff += DecodeName(answer, answerOff, answers[i].MX.name);
		answers[i].MX.addr.s_addr = 0;

		break;
                
	    case XPL_RR_A:
		aData = (ResolveAData*)(answer + answerOff);
		answers[i].A.addr.s_addr = aData->address;
		answerOff += ADATASIZE;
		
		break;

	    case XPL_RR_CNAME:
		/* Instead of giving the CNAME back to the caller, we'll be nice and do the
		 * A lookup for them. */
		answerOff += DecodeName(answer, answerOff, answers[i].A.name);
		strcpy(hostCopy, answers[i].A.name);
		MemFree(answers);
		answers = NULL;

		cnameLooping = TRUE;
		++cnameLoops;
		break;

            case XPL_RR_TXT:
		len = (int) answer[answerOff];
                strncpy(answers[i].TXT.txt, answer + answerOff + 1, len);
                break;

	    case XPL_RR_PTR: {
		//ptrData = (struct PTRData*)(answer + answerOff);
		answerOff += DecodeName(answer, answerOff, answers[i].PTR.name);
		break;
	    }

	    case XPL_RR_SOA: {
		char    *ptr;

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

static int
ResolveName (char *host, int type, char *answer, int answerLen) 
{
    int size;
    
    h_errno = 0;
    
    // FIXME: cast below is wrong.
    size = res_search(host, ns_c_in, type, (u_char *)answer, answerLen);
    
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

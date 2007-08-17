// 

#ifndef XPLDNS_H
#define XPLDNS_H

#include <xplutil.h>

#define XPLDNS_NAMELEN	 256

typedef enum {
	XPLDNS_INTERNAL_ERROR = -1,	// internal error, see errno
	XPLDNS_SUCCESS = 0,
	XPLDNS_NOT_FOUND = 1,		// authoritative host not found
	XPLDNS_TRY_AGAIN = 2,		// host not found or server failure
	XPLDNS_DNS_ERROR = 3,		// query refused/other non-recoverable
	XPLDNS_NO_DATA = 4,		// name valid, no records exist
	XPLDNS_FAIL = 100,
	XPLDNS_FAIL_CNAME_LOOP = 101
} XplDns_ResultCode;

typedef enum {
	XPLDNS_RR_A	= 0x0001,
	XPLDNS_RR_CNAME	= 0x0005,
	XPLDNS_RR_SOA	= 0x0006,
	XPLDNS_RR_PTR	= 0x000C,
	XPLDNS_RR_MX	= 0x000F,
	XPLDNS_RR_TXT	= 0x0010
} XplDns_RecordType;

typedef struct {
	char name[XPLDNS_NAMELEN + 1];	// Domain name on MX record
	char mxname[XPLDNS_NAMELEN + 1]; //  address of record
	unsigned short preference;	// Preference of record
} XplDns_MxRecord;

typedef struct {
	char name[XPLDNS_NAMELEN + 1];	// Domain name on A record
	int address;			// IP address of record
} XplDns_ARecord;

typedef struct {
	char name[XPLDNS_NAMELEN + 1];	// Domain name on PTR record
} XplDns_PtrRecord;

typedef struct {
	char name[XPLDNS_NAMELEN + 1];	// Domain name on CNAME
	char cname[XPLDNS_NAMELEN + 1];	// what it points to 
} XplDns_CnameRecord;

typedef struct {
	char mname[XPLDNS_NAMELEN + 1]; // source host
	char rname[XPLDNS_NAMELEN + 1]; // contact e-mail
	unsigned long serial;		// serial number
	unsigned long refresh;		// refresh time (secs)
	unsigned long retry;		// retry time (secs)
	unsigned long expire;		// expiry time (secs)
	unsigned long minimum;		// minimum TTL (secs)
} XplDns_SoaRecord;

typedef struct {
	char txt[XPLDNS_NAMELEN + 1];	// Text attached to record
} XplDns_TxtRecord;

typedef union {
	XplDns_ARecord A;
	XplDns_CnameRecord CNAME;
	XplDns_MxRecord MX;
	XplDns_PtrRecord PTR;
	XplDns_SoaRecord SOA;
	XplDns_TxtRecord TXT;
} XplDns_Record;

typedef struct _XplDns_RecordList XplDns_RecordList;

struct _XplDns_RecordList {
	XplDns_RecordList *next;
	XplDns_RecordType type;
	XplDns_Record record;
};

typedef struct {
	XplDns_ResultCode status;
	XplDns_RecordList *list;
} XplDns_Result;

#define XPLDNS_MAX_IP_IN_LIST 	20
typedef struct{
	XplDns_ResultCode status;
	int ip_list[XPLDNS_MAX_IP_IN_LIST + 1];
	int number;
} XplDns_IpList;

typedef struct {
	XplDns_ResultCode status;
	int preference;
	char current_mx[XPLDNS_NAMELEN + 1];
	XplDns_Result *_mx_result;
	XplDns_RecordList *_mx_current;
} XplDns_MxLookup;

int		XplDnsInit(void);
void		XplDnsResultFree(XplDns_Result *result);
XplDns_Result *	XplDnsLookupIP(const char *domain);
XplDns_MxLookup *XplDnsNewMxLookup(const char *domain);
XplDns_IpList * XplDnsNextMxLookupIpList(XplDns_MxLookup *mx);
void		XplDnsFreeMxLookup(XplDns_MxLookup *mx);

#endif

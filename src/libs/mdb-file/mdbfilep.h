/*
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
 */


#ifndef MDBP_H
#define MDBP_H

/* Little helpers */
#define MDB_CONTEXT_IS_VALID(ValueStruct)   (ValueStruct->Flags & MDB_FLAGS_CONTEXT_VALID)

/* Bit definitions */
#define MDB_FLAGS_ALLOCATED_HANDLE          (unsigned long)(1<<0)            /* The Init function has allocated the handle */
#define MDB_FLAGS_CONTEXT_VALID             (unsigned long)(1<<1)            /* The Context handle is valid */
#define MDB_FLAGS_CONTEXT_DUPLICATE         (unsigned long)(1<<2)            /* The context handle is duplicated, do not free it */
#define MDB_FLAGS_BASEDN_CHANGED            (unsigned long)(1<<3)            /* Caching uses this, if the DN of a context is changed, this is set */

#define VALUE_ALLOC_SIZE                    20
#define VALUE_BUFFER_SIZE                   (10 * 1024)

#define ERR_NO_SUCH_ENTRY                   0xFFFFFDA7
#define ERR_NO_SUCH_VALUE                   0xFFFFFDA6
#define ERR_NO_SUCH_ATTRIBUTE               0xFFFFFDA5
#define ERR_NO_SUCH_CLASS                   0xFFFFFDA4
#define ERR_NO_SUCH_PARTITION               0xFFFFFDA3
#define ERR_ENTRY_ALREADY_EXISTS            0xFFFFFDA2
#define ERR_NOT_EFFECTIVE_CLASS             0xFFFFFDA1
#define ERR_ILLEGAL_ATTRIBUTE               0xFFFFFDA0
#define ERR_MISSING_MANDATORY               0xFFFFFD9F
#define ERR_ILLEGAL_DS_NAME                 0xFFFFFD9E
#define ERR_ILLEGAL_CONTAINMENT             0xFFFFFD9D
#define ERR_CANT_HAVE_MULTIPLE_VALUES       0xFFFFFD9C
#define ERR_SYNTAX_VIOLATION                0xFFFFFD9B
#define ERR_DUPLICATE_VALUE                 0xFFFFFD9A
#define ERR_ATTRIBUTE_ALREADY_EXISTS        0xFFFFFD99
#define ERR_MAXIMUM_ENTRIES_EXIST           0xFFFFFD98
#define ERR_DATABASE_FORMAT                 0xFFFFFD97
#define ERR_INCONSISTENT_DATABASE           0xFFFFFD96
#define ERR_INVALID_COMPARISON              0xFFFFFD95
#define ERR_COMPARISON_FAILED               0xFFFFFD94
#define ERR_TRANSACTIONS_DISABLED           0xFFFFFD93
#define ERR_INVALID_TRANSPORT               0xFFFFFD92
#define ERR_SYNTAX_INVALID_IN_NAME          0xFFFFFD91
#define ERR_TRANSPORT_FAILURE               0xFFFFFD8F
#define ERR_ALL_REFERRALS_FAILED            0xFFFFFD8E
#define ERR_CANT_REMOVE_NAMING_VALUE        0xFFFFFD8D
#define ERR_OBJECT_CLASS_VIOLATION          0xFFFFFD8C
#define ERR_ENTRY_IS_NOT_LEAF               0xFFFFFD8B
#define ERR_DIFFERENT_TREE                  0xFFFFFD8A
#define ERR_SYSTEM_FAILURE                  0xFFFFFD88
#define ERR_INVALID_ENTRY_FOR_ROOT          0xFFFFFD87
#define ERR_NO_REFERRALS                    0xFFFFFD86
#define ERR_REMOTE_FAILURE                  0xFFFFFD85
#define ERR_UNREACHABLE_SERVER              0XFFFFFD84
#define ERR_NO_CHARACTER_MAPPING            0XFFFFFD82
#define ERR_INCOMPLETE_AUTHENTICATION       0XFFFFFD81
#define ERR_INVALID_CERTIFICATE             0xFFFFFD80
#define ERR_INVALID_REQUEST                 0xFFFFFD7F
#define ERR_INVALID_ITERATION               0xFFFFFD7E
#define ERR_SCHEMA_IS_NONREMOVABLE          0xFFFFFD7D
#define ERR_SCHEMA_IS_IN_USE                0xFFFFFD7C
#define ERR_CLASS_ALREADY_EXISTS            0xFFFFFD7B
#define ERR_BAD_NAMING_ATTRIBUTES           0xFFFFFD7A
#define ERR_INSUFFICIENT_STACK              0xFFFFFD78
#define ERR_INSUFFICIENT_BUFFER             0xFFFFFD77
#define ERR_AMBIGUOUS_CONTAINMENT           0xFFFFFD76
#define ERR_AMBIGUOUS_NAMING                0xFFFFFD75
#define ERR_DUPLICATE_MANDATORY             0xFFFFFD74
#define ERR_DUPLICATE_OPTIONAL              0xFFFFFD73
#define ERR_RECORD_IN_USE                   0xFFFFFD6C
#define ERR_ENTRY_NOT_CONTAINER             0xFFFFFD64
#define ERR_FAILED_AUTHENTICATION           0xFFFFFD63
#define ERR_INVALID_CONTEXT                 0xFFFFFD62
#define ERR_NO_SUCH_PARENT                  0xFFFFFD61
#define ERR_NO_ACCESS                       0xFFFFFD60
#define ERR_INVALID_NAME_SERVICE            0xFFFFFD5E
#define ERR_INVALID_TASK                    0xFFFFFD5D
#define ERR_INVALID_CONN_HANDLE             0xFFFFFD5C
#define ERR_INVALID_IDENTITY                0xFFFFFD5B
#define ERR_DUPLICATE_ACL                   0xFFFFFD5A
#define ERR_ALIAS_OF_AN_ALIAS               0xFFFFFD57
#define ERR_INVALID_RDN                     0xFFFFFD4E
#define ERR_INCORRECT_BASE_CLASS            0xFFFFFD4C
#define ERR_MISSING_REFERENCE               0xFFFFFD4B
#define ERR_LOST_ENTRY                      0xFFFFFD4A
#define ERR_FATAL                           0xFFFFFD45

typedef struct {
    /* Semi-public; known to the main MDB library */
    unsigned char Description[128];
    struct _MDBInterfaceStruct *Interface;

    /* Private */
    unsigned char Dirname[XPL_MAX_PATH + 1];
    BOOL ReadOnly;
} MDBFILEContextStruct;

typedef MDBFILEContextStruct *MDBHandle;

typedef struct {
    /* Public part, exposed to allow simpler code */
    unsigned char **Value;
    unsigned long Used;
    unsigned long ErrNo;
    struct _MDBInterfaceStruct *Interface;

    /* Private part, not exposed in public header */
    unsigned long Flags;
    unsigned long Allocated;
    unsigned char *BaseDN;
    unsigned char Buffer[VALUE_BUFFER_SIZE];
    unsigned char Path[XPL_MAX_PATH + 1];
    unsigned char Filename[(XPL_MAX_PATH * 4) + 1];

    MDBFILEContextStruct *Handle;
} MDBValueStruct;

typedef struct {
    BOOL Initialized;

    unsigned int EntriesLeft;

    FILE *File;

    MDBValueStruct *V;

    unsigned char Buffer[VALUE_BUFFER_SIZE];
    unsigned char Filename[(XPL_MAX_PATH * 4) + 1];

    unsigned long Offset;
} MDBEnumStruct;

typedef struct _MDBFILEBaseSchemaAttributes {
    unsigned char *name;
    unsigned long flags;
    unsigned long type;
    unsigned char *asn1;
} MDBFILEBaseSchemaAttributes;

typedef struct _MDBFILEBaseSchemaClasses {
    unsigned char *name;
    unsigned long flags;
    unsigned char *asn1;
    unsigned char *superClass;
    unsigned char *containedBy;
    unsigned char *namedBy;
    unsigned char *mandatory;
    unsigned char *optional;
} MDBFILEBaseSchemaClasses;

MDBFILEBaseSchemaAttributes MDBFILEBaseSchemaAttributesList[] = {
    { "ACL",64,17,"2.16.840.1.113719.1.1.4.1.2"    }, 
    { "Aliased Object Name",69,1,"2.5.4.1"    }, 
    { "Back Link",268,23,"2.16.840.1.113719.1.1.4.1.6"    }, 
    { "Bindery Property",12,32,"2.16.840.1.113719.1.1.4.1.8"    }, 
    { "Bindery Object Restriction",13,8,"2.16.840.1.113719.1.1.4.1.7"    }, 
    { "Bindery Type",45,5,"2.16.840.1.113719.1.1.4.1.9"    }, 
    { "CA Private Key",93,32,"2.16.840.1.113719.1.1.4.1.11"    }, 
    { "CA Public Key",205,32,"2.16.840.1.113719.1.1.4.1.12"    }, 
    { "Cartridge",100,3,"2.16.840.1.113719.1.1.4.1.10"    }, 
    { "CN",102,3,"2.5.4.3"    }, 
    { "Printer Configuration",69,32,"2.16.840.1.113719.1.1.4.1.78"    }, 
    { "Convergence",71,8,"2.16.840.1.113719.1.1.4.1.15"    }, 
    { "C",103,3,"2.5.4.6"    }, 
    { "Default Queue",325,1,"2.16.840.1.113719.1.1.4.1.18"    }, 
    { "Description",102,3,"2.5.4.13"    }, 
    { "Partition Creation Time",205,19,"2.16.840.1.113719.1.1.4.1.64"    }, 
    { "Facsimile Telephone Number",68,11,"2.5.4.23"    }, 
    { "High Convergence Sync Interval",69,27,"2.16.840.1.113719.1.1.4.1.117"    }, 
    { "Group Membership",580,1,"2.16.840.1.113719.1.1.4.1.25"    }, 
    { "Home Directory",71,15,"2.16.840.1.113719.1.1.4.1.26"    }, 
    { "Host Device",69,1,"2.16.840.1.113719.1.1.4.1.27"    }, 
    { "Host Resource Name",101,3,"2.16.840.1.113719.1.1.4.1.28"    }, 
    { "Host Server",69,1,"2.16.840.1.113719.1.1.4.1.29"    }, 
    { "Inherited ACL",76,17,"2.16.840.1.113719.1.1.4.1.30"    }, 
    { "L",102,3,"2.5.4.7"    }, 
    { "Login Allowed Time Map",71,32,"2.16.840.1.113719.1.1.4.1.39"    }, 
    { "Login Disabled",69,7,"2.16.840.1.113719.1.1.4.1.40"    }, 
    { "Login Expiration Time",69,24,"2.16.840.1.113719.1.1.4.1.41"    }, 
    { "Login Grace Limit",69,8,"2.16.840.1.113719.1.1.4.1.42"    }, 
    { "Login Grace Remaining",69,22,"2.16.840.1.113719.1.1.4.1.43"    }, 
    { "Login Intruder Address",69,12,"2.16.840.1.113719.1.1.4.1.44"    }, 
    { "Login Intruder Attempts",69,22,"2.16.840.1.113719.1.1.4.1.45"    }, 
    { "Login Intruder Limit",69,8,"2.16.840.1.113719.1.1.4.1.46"    }, 
    { "Intruder Attempt Reset Interval",69,27,"2.16.840.1.113719.1.1.4.1.31"    }, 
    { "Login Intruder Reset Time",69,24,"2.16.840.1.113719.1.1.4.1.47"    }, 
    { "Login Maximum Simultaneous",69,8,"2.16.840.1.113719.1.1.4.1.48"    }, 
    { "Login Script",69,21,"2.16.840.1.113719.1.1.4.1.49"    }, 
    { "Login Time",5,24,"2.16.840.1.113719.1.1.4.1.50"    }, 
    { "Member",68,1,"2.5.4.31"    }, 
    { "Memory",69,8,"2.16.840.1.113719.1.1.4.1.52"    }, 
    { "EMail Address",228,14,"0.9.2342.19200300.100.1.3"    }, 
    { "Network Address",68,12,"2.16.840.1.113719.1.1.4.1.55"    }, 
    { "Network Address Restriction",68,12,"2.16.840.1.113719.1.1.4.1.56"    }, 
    { "Notify",68,25,"2.16.840.1.113719.1.1.4.1.57"    }, 
    { "Obituary",76,32,"2.16.840.1.113719.1.1.4.1.114"    }, 
    { "Object Class",196,20,"2.5.4.0"    }, 
    { "Operator",324,1,"2.16.840.1.113719.1.1.4.1.59"    }, 
    { "OU",102,3,"2.5.4.11"    }, 
    { "O",102,3,"2.5.4.10"    }, 
    { "Owner",68,1,"2.5.4.32"    }, 
    { "Page Description Language",102,4,"2.16.840.1.113719.1.1.4.1.63"    }, 
    { "Passwords Used",86,32,"2.16.840.1.113719.1.1.4.1.65"    }, 
    { "Password Allow Change",69,7,"2.16.840.1.113719.1.1.4.1.66"    }, 
    { "Password Expiration Interval",69,27,"2.16.840.1.113719.1.1.4.1.67"    }, 
    { "Password Expiration Time",69,24,"2.16.840.1.113719.1.1.4.1.68"    }, 
    { "Password Minimum Length",69,8,"2.16.840.1.113719.1.1.4.1.69"    }, 
    { "Password Required",69,7,"2.16.840.1.113719.1.1.4.1.70"    }, 
    { "Password Unique Required",69,7,"2.16.840.1.113719.1.1.4.1.71"    }, 
    { "Path",68,15,"2.16.840.1.113719.1.1.4.1.72"    }, 
    { "Physical Delivery Office Name",102,3,"2.5.4.19"    }, 
    { "Postal Address",68,18,"2.5.4.16"    }, 
    { "Postal Code",102,3,"2.5.4.17"    }, 
    { "Postal Office Box",102,3,"2.5.4.18"    }, 
    { "Print Job Configuration",69,21,"2.16.840.1.113719.1.1.4.1.80"    }, 
    { "Printer Control",69,21,"2.16.840.1.113719.1.1.4.1.79"    }, 
    { "Private Key",93,32,"2.16.840.1.113719.1.1.4.1.82"    }, 
    { "Profile",69,1,"2.16.840.1.113719.1.1.4.1.83"    }, 
    { "Public Key",205,32,"2.16.840.1.113719.1.1.4.1.84"    }, 
    { "Queue",68,25,"2.16.840.1.113719.1.1.4.1.85"    }, 
    { "Queue Directory",359,3,"2.16.840.1.113719.1.1.4.1.86"    }, 
    { "Reference",1052,1,"2.16.840.1.113719.1.1.4.1.115"    }, 
    { "Replica",204,16,"2.16.840.1.113719.1.1.4.1.88"    }, 
    { "Resource",68,1,"2.16.840.1.113719.1.1.4.1.89"    }, 
    { "Role Occupant",68,1,"2.5.4.33"    }, 
    { "Higher Privileges",836,1,"2.16.840.1.113719.1.1.4.1.116"    }, 
    { "Security Equals",836,1,"2.16.840.1.113719.1.1.4.1.92"    }, 
    { "See Also",68,1,"2.5.4.34"    }, 
    { "Serial Number",102,4,"2.16.840.1.113719.1.1.4.1.94"    }, 
    { "Server",324,1,"2.16.840.1.113719.1.1.4.1.95"    }, 
    { "S",102,3,"2.5.4.8"    }, 
    { "Status",197,8,"2.16.840.1.113719.1.1.4.1.98"    }, 
    { "SA",102,3,"2.5.4.9"    }, 
    { "Supported Typefaces",102,3,"2.16.840.1.113719.1.1.4.1.102"    }, 
    { "Supported Services",102,3,"2.16.840.1.113719.1.1.4.1.101"    }, 
    { "Surname",230,3,"2.5.4.4"    }, 
    { "Telephone Number",100,10,"2.5.4.20"    }, 
    { "Title",102,3,"2.5.4.12"    }, 
    { "Unknown",4,0,"2.16.840.1.113719.1.1.4.1.109"    }, 
    { "User",324,1,"2.16.840.1.113719.1.1.4.1.111"    }, 
    { "Version",231,3,"2.16.840.1.113719.1.1.4.1.112"    }, 
    { "Account Balance",69,22,"2.16.840.1.113719.1.1.4.1.1"    }, 
    { "Allow Unlimited Credit",69,7,"2.16.840.1.113719.1.1.4.1.4"    }, 
    { "Low Convergence Reset Time",69,24,"2.16.840.1.113719.1.1.4.1.118"    }, 
    { "Minimum Account Balance",69,8,"2.16.840.1.113719.1.1.4.1.54"    }, 
    { "Low Convergence Sync Interval",69,27,"2.16.840.1.113719.1.1.4.1.104"    }, 
    { "Device",68,1,"2.16.840.1.113719.1.1.4.1.21"    }, 
    { "Message Server",69,1,"2.16.840.1.113719.1.1.4.1.53"    }, 
    { "Language",69,6,"2.16.840.1.113719.1.1.4.1.34"    }, 
    { "Supported Connections",69,8,"2.16.840.1.113719.1.1.4.1.100"    }, 
    { "Type Creator Map",69,21,"2.16.840.1.113719.1.1.4.1.107"    }, 
    { "UID",69,8,"2.16.840.1.113719.1.1.4.1.108"    }, 
    { "GID",69,8,"2.16.840.1.113719.1.1.4.1.24"    }, 
    { "Unknown Base Class",39,3,"2.16.840.1.113719.1.1.4.1.110"    }, 
    { "Received Up To",204,19,"2.16.840.1.113719.1.1.4.1.87"    }, 
    { "Synchronized Up To",1164,19,"2.16.840.1.113719.1.1.4.1.33"    }, 
    { "Authority Revocation",77,32,"2.16.840.1.113719.1.1.4.1.5"    }, 
    { "Certificate Revocation",77,32,"2.16.840.1.113719.1.1.4.1.13"    }, 
    { "Cross Certificate Pair",68,32,"2.16.840.1.113719.1.1.4.1.17"    }, 
    { "Locked By Intruder",69,7,"2.16.840.1.113719.1.1.4.1.37"    }, 
    { "Printer",68,25,"2.16.840.1.113719.1.1.4.1.77"    }, 
    { "Detect Intruder",69,7,"2.16.840.1.113719.1.1.4.1.20"    }, 
    { "Lockout After Detection",69,7,"2.16.840.1.113719.1.1.4.1.38"    }, 
    { "Intruder Lockout Reset Interval",69,27,"2.16.840.1.113719.1.1.4.1.32"    }, 
    { "Server Holds",68,26,"2.16.840.1.113719.1.1.4.1.96"    }, 
    { "SAP Name",103,3,"2.16.840.1.113719.1.1.4.1.91"    }, 
    { "Volume",69,1,"2.16.840.1.113719.1.1.4.1.113"    }, 
    { "Last Login Time",13,24,"2.16.840.1.113719.1.1.4.1.35"    }, 
    { "Print Server",69,25,"2.16.840.1.113719.1.1.4.1.81"    }, 
    { "NNS Domain",102,3,"2.16.840.1.113719.1.1.4.1.119"    }, 
    { "Full Name",102,3,"2.16.840.1.113719.1.1.4.1.120"    }, 
    { "Partition Control",204,25,"2.16.840.1.113719.1.1.4.1.121"    }, 
    { "Revision",2189,22,"2.16.840.1.113719.1.1.4.1.122"    }, 
    { "Certificate Validity Interval",71,27,"2.16.840.1.113719.1.1.4.1.123"    }, 
    { "External Synchronizer",68,32,"2.16.840.1.113719.1.1.4.1.124"    }, 
    { "Messaging Database Location",69,15,"2.16.840.1.113719.1.1.4.1.125"    }, 
    { "Message Routing Group",68,1,"2.16.840.1.113719.1.1.4.1.126"    }, 
    { "Messaging Server",68,1,"2.16.840.1.113719.1.1.4.1.127"    }, 
    { "Postmaster",68,1,"2.16.840.1.113719.1.1.4.1.128"    }, 
    { "Mailbox Location",197,1,"2.16.840.1.113719.1.1.4.1.162"    }, 
    { "Mailbox ID",231,3,"2.16.840.1.113719.1.1.4.1.163"    }, 
    { "External Name",69,32,"2.16.840.1.113719.1.1.4.1.164"    }, 
    { "Security Flags",69,8,"2.16.840.1.113719.1.1.4.1.165"    }, 
    { "Messaging Server Type",103,3,"2.16.840.1.113719.1.1.4.1.166"    }, 
    { "Last Referenced Time",1029,19,"2.16.840.1.113719.1.1.4.1.167"    }, 
    { "Given Name",230,3,"2.5.4.42"    }, 
    { "Initials",230,3,"2.5.4.43"    }, 
    { "Generational Qualifier",231,3,"2.16.840.1.113719.1.1.4.1.170"    }, 
    { "Profile Membership",580,1,"2.16.840.1.113719.1.1.4.1.171"    }, 
    { "DS Revision",197,8,"2.16.840.1.113719.1.1.4.1.172"    }, 
    { "Supported Gateway",102,3,"2.16.840.1.113719.1.1.4.1.173"    }, 
    { "Equivalent To Me",324,1,"2.16.840.1.113719.1.1.4.1.174"    }, 
    { "Replica Up To",1164,32,"2.16.840.1.113719.1.1.4.1.175"    }, 
    { "Partition Status",1164,32,"2.16.840.1.113719.1.1.4.1.176"    }, 
    { "Permanent Config Parms",196,32,"2.16.840.1.113719.1.1.4.1.177"    }, 
    { "Timezone",133,32,"2.16.840.1.113719.1.1.4.1.178"    }, 
    { "Bindery Restriction Level",5,8,"2.16.840.1.113719.1.1.4.1.179"    }, 
    { "Transitive Vector",2252,32,"2.16.840.1.113719.1.1.4.1.180"    }, 
    { "T",102,3,"2.16.840.1.113719.1.1.4.1.181"    }, 
    { "Purge Vector",3212,19,"2.16.840.1.113719.1.1.4.1.183"    }, 
    { "Synchronization Tolerance",196,19,"2.16.840.1.113719.1.1.4.1.184"    }, 
    { "Password Management",69,0,"2.16.840.1.113719.1.1.4.1.185"    }, 
    { "Used By",332,15,"2.16.840.1.113719.1.1.4.1.186"    }, 
    { "Uses",332,15,"2.16.840.1.113719.1.1.4.1.187"    }, 
    { "Obituary Notify",76,32,"2.16.840.1.113719.1.1.4.1.500"    }, 
    { "GUID",207,32,"2.16.840.1.113719.1.1.4.1.501"    }, 
    { "Other GUID",198,32,"2.16.840.1.113719.1.1.4.1.502"    }, 
    { NULL,0,0,NULL    }, 
};

MDBFILEBaseSchemaClasses MDBFILEBaseSchemaClassesList[] = {
    { "[Nothing]",3,NULL,NULL,NULL,NULL,NULL,NULL    }, 
    { "Top",3,"2.5.6.0",NULL,NULL,"T","Object Class,T","CA Public Key,Private Key,Certificate Validity Interval,Authority Revocation,Last Referenced Time,Equivalent To Me,ACL,Back Link,Bindery Property,Obituary,Reference,Revision,Cross Certificate Pair,Certificate Revocation,Used By,GUID,Other GUID"    }, 
    { "Tree Root",3,"2.16.840.1.113719.1.1.6.1.32","Top","[Nothing]",NULL,NULL,NULL    }, 
    { "Alias",2,"2.5.6.1","Top",NULL,NULL,"Aliased Object Name",NULL    }, 
    { "Country",3,"2.5.6.2","Top","Top,Tree Root,[Nothing]","C","C","Description"    }, 
    { "Locality",3,"2.5.6.3","Top","Locality,Country,Organizational Unit,Organization","L,S",NULL,"Description,L,See Also,S,SA"    }, 
    { "Organization",3,"2.5.6.4",NULL,"Top,Tree Root,Country,Locality,[Nothing]","O","O","Description,Facsimile Telephone Number,L,Login Script,EMail Address,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,Print Job Configuration,Printer Control,See Also,S,SA,Telephone Number,Login Intruder Limit,Intruder Attempt Reset Interval,Detect Intruder,Lockout After Detection,Intruder Lockout Reset Interval"    }, 
    { "Organizational Unit",3,"2.5.6.5",NULL,"Locality,Organization,Organizational Unit","OU","OU","Description,Facsimile Telephone Number,L,Login Script,EMail Address,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,Print Job Configuration,Printer Control,See Also,S,SA,Telephone Number,Login Intruder Limit,Intruder Attempt Reset Interval,Detect Intruder,Lockout After Detection,Intruder Lockout Reset Interval"    }, 
    { "Organizational Role",2,"2.5.6.8","Top","Organization,Organizational Unit","CN","CN","Description,Facsimile Telephone Number,L,EMail Address,OU,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,Role Occupant,See Also,S,SA,Telephone Number,Mailbox Location,Mailbox ID"    }, 
    { "Group",2,"2.5.6.17","Top","Organization,Organizational Unit","CN","CN","Description,L,Member,OU,O,Owner,See Also,GID,Full Name,EMail Address,Mailbox Location,Mailbox ID,Profile,Profile Membership,Login Script"    }, 
    { "Person",2,"2.5.6.6",NULL,"Organization,Organizational Unit","CN","CN","Description,See Also,Telephone Number,Full Name,Given Name,Initials,Generational Qualifier"    }, 
    { "Organizational Person",2,"2.5.6.7","Person","Organization,Organizational Unit","CN,OU",NULL,"Facsimile Telephone Number,L,EMail Address,OU,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,S,SA,Title,Mailbox Location,Mailbox ID"    }, 
    { "User",2,"2.5.6.10","Organizational Person",NULL,NULL,NULL,"Group Membership,Home Directory,Login Allowed Time Map,Login Disabled,Login Expiration Time,Login Grace Limit,Login Grace Remaining,Login Intruder Address,Login Intruder Attempts,Login Intruder Reset Time,Login Maximum Simultaneous,Login Script,Login Time,Network Address Restriction,Network Address,Passwords Used,Password Allow Change,Password Expiration Interval,Password Expiration Time,Password Minimum Length,Password Required,Password Unique Required,Print Job Configuration,Private Key,Profile,Public Key,Security Equals,Account Balance,Allow Unlimited Credit,Minimum Account Balance,Message Server,Language,UID,Locked By Intruder,Server Holds,Last Login Time,Type Creator Map,Higher Privileges,Printer Control,Security Flags,Profile Membership,Timezone"    }, 
    { "Device",2,"2.16.840.1.113719.1.1.6.1.6","Top","Organization,Organizational Unit","CN","CN","Description,L,Network Address,OU,O,Owner,See Also,Serial Number"    }, 
    { "Computer",2,"2.16.840.1.113719.1.1.6.1.4","Device",NULL,NULL,NULL,"Operator,Server,Status"    }, 
    { "Printer",2,"2.16.840.1.113719.1.1.6.1.17","Device",NULL,NULL,NULL,"Cartridge,Printer Configuration,Default Queue,Host Device,Print Server,Memory,Network Address Restriction,Notify,Operator,Page Description Language,Queue,Status,Supported Typefaces"    }, 
    { "Resource",0,"2.16.840.1.113719.1.1.6.1.21","TOp","Organization,Organizational Unit","CN","CN","Description,Host Resource Name,L,OU,O,See Also,Uses"    }, 
    { "Queue",2,"2.16.840.1.113719.1.1.6.1.20","Resource",NULL,NULL,"Queue Directory","Device,Operator,Server,User,Network Address,Volume,Host Server"    }, 
    { "Bindery Queue",2,"2.16.840.1.113719.1.1.6.1.3","Queue",NULL,"CN","Bindery Type",NULL    }, 
    { "Volume",2,"2.16.840.1.113719.1.1.6.1.26","Resource",NULL,NULL,"Host Server","Status"    }, 
    { "Directory Map",2,"2.16.840.1.113719.1.1.6.1.7","Resource",NULL,NULL,"Host Server","Path"    }, 
    { "Profile",2,"2.16.840.1.113719.1.1.6.1.19","Top","Organization,Organizational Unit","CN","CN,Login Script","Description,L,OU,O,See Also,Full Name"    }, 
    { "Server",0,"2.16.840.1.113719.1.1.6.1.22","Top","Organization,Organizational Unit","CN","CN","Description,Host Device,L,OU,O,Private Key,Public Key,Resource,See Also,Status,User,Version,Network Address,Account Balance,Allow Unlimited Credit,Minimum Account Balance,Full Name,Security Equals,Security Flags,Timezone"    }, 
    { "NCP Server",2,"2.16.840.1.113719.1.1.6.1.10","Server",NULL,NULL,NULL,"Operator,Supported Services,Messaging Server,DS Revision,Permanent Config Parms"    }, 
    { "Print Server",2,"2.16.840.1.113719.1.1.6.1.18","Server",NULL,NULL,NULL,"Operator,Printer,SAP Name"    }, 
    { "CommExec",2,"2.16.840.1.113719.1.1.6.1.31","Server",NULL,NULL,NULL,"Network Address Restriction"    }, 
    { "Bindery Object",2,"2.16.840.1.113719.1.1.6.1.2","Top","Organization,Organizational Unit","CN,Bindery Type","Bindery Object Restriction,Bindery Type,CN",NULL    }, 
    { "Unknown",3,"2.16.840.1.113719.1.1.6.1.24","Top",NULL,NULL,NULL,NULL    }, 
    { "Partition",0,"2.16.840.1.113719.1.1.6.1.15",NULL,NULL,NULL,NULL,"Convergence,Partition Creation Time,Replica,Inherited ACL,Low Convergence Sync Interval,Received Up To,Synchronized Up To,Authority Revocation,Certificate Revocation,CA Private Key,CA Public Key,Cross Certificate Pair,Low Convergence Reset Time,High Convergence Sync Interval,Partition Control,Replica Up To,Partition Status,Transitive Vector,Purge Vector,Synchronization Tolerance,Obituary Notify,Local Received Up To"    }, 
    { "AFP Server",2,"2.16.840.1.113719.1.1.6.1.0","Server",NULL,NULL,NULL,"Serial Number,Supported Connections"    }, 
    { "Messaging Server",2,"2.16.840.1.113719.1.1.6.1.27","Server",NULL,NULL,NULL,"Messaging Database Location,Message Routing Group,Postmaster,Supported Services,Messaging Server Type,Supported Gateway"    }, 
    { "Message Routing Group",2,"2.16.840.1.113719.1.1.6.1.28","Group",NULL,NULL,NULL,NULL    }, 
    { "External Entity",2,"2.16.840.1.113719.1.1.6.1.29","Top","Organization,Organizational Unit","CN,OU","CN","Description,See Also,Facsimile Telephone Number,L,EMail Address,OU,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,S,SA,Title,External Name,Mailbox Location,Mailbox ID"    }, 
    { "List",2,"2.16.840.1.113719.1.1.6.1.30","Top","Organization,Organizational Unit","CN","CN","Description,L,Member,OU,O,EMail Address,Mailbox Location,Mailbox ID,Owner,See Also,Full Name"    }, 
    { NULL,0,NULL,NULL,NULL,NULL,NULL,NULL    }
};

#endif /* MDBP_H */

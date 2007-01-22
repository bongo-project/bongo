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

#include "config.h"
#include <xpl.h>
#include <connio.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <mdb.h>
#include <memmgr.h>

#define EXT_USAGE_MSG "Usage: mdbtool [options] <command>\n\n"\
"Options:\n"\
"-h, --help      Display this message\n"\
"-p, --password  Password for authentication\n"\
"-u, --username  Username for authentication\n\n"\
"Commands:\n"\
"getbasedn\n"\
"getattrs <object> <attrname>\n"\
"getdnattrs <object> <attrname>\n"\
"addattr <object> <attrname> <value>\n"\
"setattr <object> <attrname> <value>\n"\
"addobject <object> <class> [attr1=value1 attr2=...]\n"\
"delobject <object> [true|false]\n"\
"listobjects <object>\n"\
"setpassword <object> <password|-f pwfile>\n"\
"verifypassword <object> <password>\n"\
"isobject <object>\n"\
"defattr <attrname> <asn1> [opt1=val1 opt2=...]\n"\
"defclass <classname> <asn1> [opt1=val1 opt2=...]\n"\
"defclassattr <classname> <attrname>\n"\
"delattrdef <attrname>\n"\
"delclassdef <classname>\n"\
"grantobj <object> <trustee> [opt1=val1 opt2=...]\n"\
"grantattr <object> <attrname> <trustee> [opt1=val1 opt2=...]\n"\
"gethostipaddr\n"

struct dict {
    void *next;
    unsigned char *key;
    unsigned char *value;
};

void DumpAttr(MDBHandle *handle, MDBValueStruct *vs, 
        const unsigned char *object);
BOOL GetBaseDN(MDBHandle *h);
BOOL GetAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL GetDnAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL AddAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL SetAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL AddObject(MDBHandle *h, char **argv, int argc, int next_arg); 
BOOL IsObject(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL ListObjects(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL SetPassword(MDBHandle *h, char **argv, int argc, int next_arg); 
BOOL VerifyPassword(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL DelObject(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL DefAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL DefClass(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL DefClassAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL DelAttrDef(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL DelClassDef(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL GrantObj(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL GrantAttr(MDBHandle *h, char **argv, int argc, int next_arg);
BOOL GetHostIpAddr(void);
struct dict *GetDict(char *list[], int items);
void FreeDict(struct dict *d);

int
main(int argc, char *argv[])
{
    char *command = NULL;
    unsigned char *auth_username = NULL;
    unsigned char *auth_password = NULL;
    MDBHandle *h = NULL;
    int next_arg = 1;
    BOOL result = TRUE;

    /* parse options */    
    while (next_arg < argc && argv[next_arg][0] == '-') {
        if (!strcmp(argv[next_arg], "-h") || 
                !strcmp(argv[next_arg], "--help")) {
            printf(EXT_USAGE_MSG);
            return 1;
        } else if (!strcmp(argv[next_arg], "-u") || 
                !strcmp(argv[next_arg], "--username")) {
            if (next_arg + 1 < argc) {
                auth_username = argv[++next_arg];
            } else {
                printf("mdbtool: Username must follow --username flag.\n");
                return 1;
            }
        } else if (!strcmp(argv[next_arg], "-p") || 
                !strcmp(argv[next_arg], "--password")) {
            if (next_arg + 1 < argc) {
                auth_password = argv[++next_arg];
            } else {
                printf("mdbtool: Password must follow --password flag.\n");
                return 1;
            }
        } else {
            printf("mdbtool: Unrecognized option: %s\n", argv[next_arg]);
            return 1;
        }

        next_arg++;
    }
    
    if (next_arg > (argc - 1)) {
        printf("mdbtool: No command specified (--help for command list).\n");
        return 1;
    }
    
    command = argv[next_arg++];

    /* MDB-FILE uses the memory manager.  Otherwise we don't need this. */
    if (!MemoryManagerOpen("mdbtool")) {
        printf("ERROR: Could not initialize memory manager\n");
        return 2;
    }
    
    if (!MDBInit()) {
        printf("ERROR: Could not initialise MDB\n");
        MemoryManagerClose("mdbtool");
        return 2;
    }
    
    h = MDBAuthenticate((unsigned char*) "Bongo", auth_username, auth_password);

    if (h == NULL) {
        printf("ERROR: Could not authenticate to MDB\n");
        MDBShutdown();
        MemoryManagerClose("mdbtool");
        return 2;
    }
    
    if (!strcmp(command, "getbasedn")) {
        result = GetBaseDN(h);
    } else if (!strcmp(command, "getattrs")) {
        result = GetAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "getdnattrs")) {
        result = GetDnAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "addattr")) {
        result = AddAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "setattr")) {
        result = SetAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "addobject")) {
        result = AddObject(h, argv, argc, next_arg);
    } else if (!strcmp(command, "delobject")) {
        result = DelObject(h, argv, argc, next_arg);
    } else if (!strcmp(command, "listobjects")) {
        result = ListObjects(h, argv, argc, next_arg);
    } else if (!strcmp(command, "setpassword")) {
        result = SetPassword(h, argv, argc, next_arg);
    } else if (!strcmp(command, "verifypassword")) {
        result = VerifyPassword(h, argv, argc, next_arg);
    } else if (!strcmp(command, "isobject")) {
        result = IsObject(h, argv, argc, next_arg);
    } else if (!strcmp(command, "defattr")) {
        result = DefAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "defclass")) {
        result = DefClass(h, argv, argc, next_arg);
    } else if (!strcmp(command, "defclassattr")) {
        result = DefClassAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "delattrdef")) {
        result = DelAttrDef(h, argv, argc, next_arg);
    } else if (!strcmp(command, "delclassdef")) {
        result = DelClassDef(h, argv, argc, next_arg);
    } else if (!strcmp(command, "grantobj")) {
        result = GrantObj(h, argv, argc, next_arg);
    } else if (!strcmp(command, "grantattr")) {
        result = GrantAttr(h, argv, argc, next_arg);
    } else if (!strcmp(command, "gethostipaddr")) {
        result = GetHostIpAddr();
    } else {
        printf("mdbtool: Unknown command: %s\n", command);
        result = FALSE;
    }

    MDBRelease(h);
    MDBShutdown();
    MemoryManagerClose("mdbtool");
    return (result == FALSE);
}

BOOL
GetBaseDN(MDBHandle *h)
{
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        printf("%s\n", MDBGetBaseDN(vs));
        MDBDestroyValueStruct(vs);
        result = TRUE;
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }

    return result;
}

BOOL
GetAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *attrname = NULL;
    MDBValueStruct *vs = NULL;
    int i, l;
    BOOL result = TRUE;
    
    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool getattrs <object> <attrname>\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    attrname = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if ((l = MDBRead(object, attrname, vs)))  {
            for (i = 0; i < l; i++) {
                printf ("%s\n", vs->Value[i]);
            }
        } else {
            printf("mdbtool: Could not retrieve value or attribute does not "
                    "exist.\n");
            result = FALSE;
        }
    
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    return result;
}

BOOL
GetDnAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *attrname = NULL;    
    MDBValueStruct *vs = NULL;
    int i, l;
    BOOL result = TRUE;

    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool getdnattrs <object> <attrname>\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    attrname = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if ((l = MDBReadDN(object, attrname, vs))) {
            for (i = 0; i < l; i++) {
                printf ("%s\n", vs->Value[i]);
            }
        } else {
            printf("mdbtool: Could not retrieve value or attribute does not "
                    "exist.\n");
            result = FALSE;
        }

        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }

    return result;
}

BOOL
AddAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    char *object = NULL;
    char *attrname = NULL;
    char *value = NULL;
    MDBValueStruct *vs;
    BOOL result = TRUE;
    
    if (next_arg > (argc - 3)) {
        printf("Usage: mdbtool addattr <object> <attrname> <value>\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    attrname = (unsigned char *) argv[next_arg++];
    value = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if (strchr(value, '\\')) {
            result = MDBAddDN(object, attrname, value, vs);
        } else {
            result = MDBAdd(object, attrname, value, vs);
        }

        if (!result) {
            printf("mdbtool: Could not add attribute value.\n");
        }
        
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }

    return result;
}

BOOL
SetAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *attrname = NULL;
    unsigned char *value = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;
        
    if (next_arg > (argc - 3)) {
        printf("Usage: mdbtool setattr <object> <attrname> <value>\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    attrname = (unsigned char *) argv[next_arg++];
    value = (unsigned char *) argv[next_arg++];

    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if (MDBAddValue(value, vs)) {
            if (strchr(value, '\\')) {
                result = MDBWriteDN(object, attrname, vs);
            } else {
                result = MDBWrite(object, attrname, vs);
            }
        }

        if (!result) {
            printf("mdbtool: Could not set attribute value.\n");
        }
        
        MDBDestroyValueStruct(vs);    
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    return result;
}

BOOL
AddObject(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *class = NULL;
    struct dict *attrs = NULL;
    struct dict *attr = NULL;
    MDBValueStruct *attvs = NULL;
    MDBValueStruct *valvs = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool addobject <object> <class> [attr1=value1 "
                "attr2=...]\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    class = (unsigned char *) argv[next_arg++];
    attrs = GetDict(&argv[next_arg], argc - next_arg);
    attr = attrs;

    attvs = MDBCreateValueStruct(h, NULL);
    valvs = MDBCreateValueStruct(h, NULL);
    vs = MDBCreateValueStruct(h, NULL);
    
    if (attvs && valvs && vs) {
        while (attr) {
            if (attr->key && attr->value) {
                if (strchr(attr->value, '\\')) {
                    MDBAddDNAttribute(attr->key, attr->value, attvs, valvs);
                } else {
                    MDBAddStringAttribute(attr->key, attr->value, attvs, valvs);
                }
            }

            /* Not sure how to check for errors with the above macros
            if (!result) {
                printf("ERROR: Could not add attribute.\n");
                break;
            }
            */

            attr = attr->next;
        }
    
        if (result) {
            result = MDBCreateObject(object, class, attvs, valvs, vs);

            if (!result) {
                printf("mdbtool: Could not create object.\n");
            }
        }
    
        MDBDestroyValueStruct(vs);
        MDBDestroyValueStruct(valvs);
        MDBDestroyValueStruct(attvs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    FreeDict(attrs);
    return result;
}

BOOL
SetPassword(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *password = NULL;
    unsigned char *arg_ptr = NULL;
    int next_char = 0;
    int char_count = 0;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;
    FILE *pwfile = NULL;
    
    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool setpassword <object> <password|-f pwfile>\n");
        return FALSE;
    }
    
    object = (unsigned char *) argv[next_arg++];
    arg_ptr = (unsigned char *) argv[next_arg++];
        
    if (!strcmp(arg_ptr, "-f") && next_arg < argc) {
        arg_ptr = (unsigned char *) argv[next_arg++];

        if ((pwfile = fopen(arg_ptr, "r"))) {
            if ((password = malloc(128))) {
                while ((next_char = fgetc(pwfile)) != EOF && char_count < 128) {
                    password[char_count++] = next_char;
                }

                if (next_char != EOF) {
                    printf("ERROR: Password file too long (128 bytes max).\n");
                    result = FALSE;
                }
            } else {
                printf("ERROR: Could not allocate memory for password.\n");
                result = FALSE;
            }

            fclose(pwfile);
        } else {
            printf("ERROR: Could not open password file.\n");
            result = FALSE;
        }
    } else {
        password = arg_ptr;
        arg_ptr = NULL;
    }   
   
    if (result) { 
        if ((vs = MDBCreateValueStruct(h, NULL))) {
            result = MDBChangePasswordEx(object, NULL, password, vs);

            if (!result) {
                printf("mdbtool: Could not set password.\n");
            }
        
            MDBDestroyValueStruct(vs);
        } else {
            printf("ERROR: Could not allocate MDB resources.\n");
            result = FALSE;
        }
    }

    if (arg_ptr && password) {
        free(password);
    }
    
    return result;
}

BOOL
VerifyPassword(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *password = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;
    
    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool verifypassword <object> <password>\n");
        return FALSE;
    }
    
    object = (unsigned char *) argv[next_arg++];
    password = (unsigned char *) argv[next_arg++];

    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if (!(result = MDBVerifyPassword(object, password, vs))) {
            printf("Could not verify password.\n");
        }
        
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    return result;
}

BOOL
IsObject(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 1)) {
        printf("Usage: mdbtool isobject <object>\n");
        return FALSE;
    }
    
    object = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if (MDBIsObject(object, vs)) {
            printf("%s\n", object);
        }
    
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    return result;
}

BOOL
DelObject(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *rec_arg = NULL;
    MDBValueStruct *vs = NULL;
    BOOL recursive = FALSE;
    BOOL result = TRUE;

    if (next_arg > (argc - 1)) {
        printf("Usage: mdbtool delobject <object> [true|false]\n");
        return FALSE;
    }
    
    object = (unsigned char *) argv[next_arg++];

    if (next_arg < argc) {
        rec_arg = (unsigned char *) argv[next_arg++];
        
        if (!strcasecmp(rec_arg, "true")) {
            recursive = TRUE;
        } else if (strcasecmp(rec_arg, "false")) {
            printf("mdbtool: Unrecognized option: %s\n", rec_arg);
            result = FALSE;
        }
    }

    if (result) {
        if ((vs = MDBCreateValueStruct(h, NULL))) {
            result = MDBDeleteObject(object, recursive, vs);

            if (!result) {
                printf("mdbtool: Could not delete object\n");
            }
        
            MDBDestroyValueStruct(vs);
        } else {
            printf("ERROR: Could not allocate MDB resources.\n");
            result = FALSE;
        }
    }
    
    return result;
}

BOOL
ListObjects(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;
    int i, count;

    if (next_arg > (argc - 1)) {
        printf("Usage: mdbtool listobjects <object>\n");
        return FALSE;
    }
    
    object = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        if (count = MDBEnumerateObjects(object, NULL, NULL, vs)) {
            for (i = 0; i < count; i++) {
                printf("%s\n", vs->Value[i]);
            }
        }
    
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    return result;
}

BOOL
DefAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *attrname = NULL;
    unsigned char *asn1 = NULL;
    unsigned long type = MDB_ATTR_SYN_STRING;
    BOOL single = FALSE;
    BOOL sync = FALSE;
    BOOL public = FALSE;
    MDBValueStruct *vs = NULL;
    struct dict *attrs = NULL;
    struct dict *attr = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool defattr <attrname> <asn1> [opt1=val1 "
                "opt2=...]\n");
        return FALSE;
    }

    attrname = (unsigned char *) argv[next_arg++];
    asn1 = (unsigned char *) argv[next_arg++];    
    attrs = GetDict(&argv[next_arg], argc - next_arg);
    attr = attrs;
        
    while (result && attr) {
        if (attr->key && attr->value) {
            if (!strcmp(attr->key, "type")) {
                if (!strcasecmp(attr->value, "dn")) {
                    type = MDB_ATTR_SYN_DIST_NAME;
                } else if (strcasecmp(attr->value, "string")) {
                    printf("mdbtool: Option 'type' invalid (must be 'string' "
                            "or 'dn').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "singleval")) {
                if (!strcasecmp(attr->value, "true")) {
                    single = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'singleval' invalid (must be "
                            "'true' or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "immsync")) {
                if (!strcasecmp(attr->value, "true")) {
                    sync = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'immsync' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "public")) {
                if (!strcasecmp(attr->value, "true")) {
                    public = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'public' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else {
                printf("mdbtool: Unsupported option '%s'\n", attr->key);
                result = FALSE;
            }
        }
    
        attr = attr->next;
    }
    
    if (result) {
        if ((vs = MDBCreateValueStruct(h, NULL))) {
            result = MDBDefineAttribute(attrname, asn1, type, single, sync, 
                    public, vs);

            if (!result) {
                printf("mdbtool: Could not define attribute.\n");
            }
        
            MDBDestroyValueStruct(vs);
        } else {
            printf("ERROR: Could not allocate MDB resources.\n");
            result = FALSE;
        }
    }

    FreeDict(attrs);
    return result;
}

BOOL
DefClass(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *classname = NULL;
    unsigned char *asn1 = NULL;
    //unsigned char *superclass = NULL;
    unsigned char *naming = NULL;
    BOOL container = FALSE;
    struct dict *attrs = NULL;
    struct dict *attr = NULL;
    MDBValueStruct *vs = NULL;
    MDBValueStruct *supervs = NULL;
    MDBValueStruct *contvs = NULL;
    MDBValueStruct *namvs = NULL;
    MDBValueStruct *mandvs = NULL;
    MDBValueStruct *optvs = NULL;
    BOOL result = TRUE;
    char *nptr = NULL;
    char *tptr = NULL;

    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool defclass <classname> <asn1> [opt1=val1 "
                "opt2=...]\n");
        return FALSE;
    }
    
    classname = (unsigned char *) argv[next_arg++];
    asn1 = (unsigned char *) argv[next_arg++];
    attrs = GetDict(&argv[next_arg], argc - next_arg);
    attr = attrs;

    vs = MDBCreateValueStruct(h, NULL);
    supervs = MDBCreateValueStruct(h, NULL);
    contvs = MDBCreateValueStruct(h, NULL);
    namvs = MDBCreateValueStruct(h, NULL);
    mandvs = MDBCreateValueStruct(h, NULL);
    optvs = MDBCreateValueStruct(h, NULL);

    if (vs && supervs && contvs && namvs && mandvs && optvs) { 
        while (result && attr) {
            if (attr->key && attr->value) {
                if (!strcmp(attr->key, "container")) {
                    if (!strcasecmp(attr->value, "true")) {
                        container = TRUE;
                    } else if (strcasecmp(attr->value, "false")) {
                        printf("mdbtool: Option 'container' invalid (must be "
                                "'true' or 'false').\n");
                        result = FALSE;
                    }
                } else if (!strcmp(attr->key, "superclasses")) {
                    nptr = attr->value;
		    tptr = attr->value;

                    while (result && (nptr = strchr(nptr, ','))) {
                        *nptr++ = '\0';
                        result = MDBAddValue(tptr, supervs);
			tptr = nptr;
                    }

                    result = MDBAddValue(tptr, supervs);
                } else if (!strcmp(attr->key, "containers")) {
                    nptr = attr->value;
		    tptr = attr->value;

                    while (result && (nptr = strchr(nptr, ','))) {
                        *nptr++ = '\0';
                        result = MDBAddValue(tptr, contvs);
			tptr = nptr;
                    }

                    result = MDBAddValue(tptr, contvs);
                } else if (!strcmp(attr->key, "naming")) {
                    naming = attr->value;
                } else if (!strcmp(attr->key, "requires")) {
                    nptr = attr->value;
		    tptr = attr->value;

                    while (result && (nptr = strchr(nptr, ','))) {
                        *nptr++ = '\0';
                        result = MDBAddValue(tptr, mandvs);
			tptr = nptr;
                    }

                    result = MDBAddValue(tptr, mandvs);
                } else if (!strcmp(attr->key, "allows")) {
                    nptr = attr->value;
		    tptr = attr->value;

                    while (result && (nptr = strchr(nptr, ','))) {
                        *nptr++ = '\0';
                        result = MDBAddValue(tptr, optvs);
			tptr = nptr;
                    }

                    result = MDBAddValue(tptr, optvs);
                } else {
                    printf("mdbtool: Unknown option: %s\n", attr->key);
                    result = FALSE;
                }

                attr = attr->next;
            }
        }

        if (result) {
            //if (superclass) {
            if (supervs->Used > 0) {
                //result = MDBAddValue(superclass, supervs);

                if (result && naming) {
                    result = MDBAddValue(naming, namvs);
                }

                if (result) {
                    result = MDBDefineClass(classname, asn1, container, 
                            supervs, contvs, namvs, mandvs, optvs, vs);

                    if (!result) {
                        printf("mdbtool: Could not define class.\n");
                    }
                } else {
                    printf("ERROR: Could not add definition data.\n");
                }
            } else {
                printf("mdbtool: Option 'superclasses' is required.\n");
	            result = FALSE;
            }
        }

        MDBDestroyValueStruct(supervs);
        MDBDestroyValueStruct(contvs);
        MDBDestroyValueStruct(namvs);
        MDBDestroyValueStruct(mandvs);
        MDBDestroyValueStruct(optvs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }
    
    FreeDict(attrs);
    return result;
}

BOOL
DefClassAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *classname = NULL;
    unsigned char *attrname = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 2)) {
        printf("Usage: mdbtool defclassattr <classname> <attrname>\n");
        return FALSE;
	}
    
    classname = (unsigned char *) argv[next_arg++];
    attrname = (unsigned char *) argv[next_arg++];

    if ((vs = MDBCreateValueStruct(h, NULL))) {
        result = MDBAddAttribute(attrname, classname, vs);

        if (!result) {
            printf("mdbtool: Could not add attribute to class.\n");
        }
        
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }

    return result;
}

BOOL
DelAttrDef(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *attrname = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 1)) {
        printf("Usage: mdbtool delattrdef <attrname>\n");
        return FALSE;
    }
    
    attrname = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        result = MDBUndefineAttribute(attrname, vs);

        if (!result) {
            printf("mdbtool: Could not remove attribute definition.\n");
        }
        
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }

    return result;
}

BOOL
DelClassDef(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *classname = NULL;
    MDBValueStruct *vs = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 1)) {
        printf("Usage: mdbtool delclassdef <classname>\n");
        return FALSE;
    }
    
    classname = (unsigned char *) argv[next_arg++];
    
    if ((vs = MDBCreateValueStruct(h, NULL))) {
        result = MDBUndefineClass(classname, vs);

        if (!result) {
            printf("mdbtool: Could not remove class definition.\n");
        }
        
        MDBDestroyValueStruct(vs);
    } else {
        printf("ERROR: Could not allocate MDB resources.\n");
        result = FALSE;
    }

    return result;
}

BOOL
GrantObj(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *trustee = NULL;
    BOOL read = FALSE;
    BOOL write = FALSE;
    BOOL delete = FALSE;
    BOOL rename = FALSE;
    BOOL admin = FALSE;
    MDBValueStruct *vs = NULL;
    struct dict *attrs = NULL;
    struct dict *attr = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 2)) {
        printf("Usage mdbtool grantobj <object> <trustee> [opt1=val1 "
                "opt2=...]\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    trustee = (unsigned char *) argv[next_arg++];

    attrs = GetDict(&argv[next_arg], argc - next_arg);
    attr = attrs;

    while (result && attr) {
        if (attr->key && attr->value) {
            if (!strcmp(attr->key, "read")) {
                if (!strcasecmp(attr->value, "true")) {
                    read = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'read' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "write")) {
                if (!strcasecmp(attr->value, "true")) {
                    write =  TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'write' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "delete")) {
                if (!strcasecmp(attr->value, "true")) {
                    delete = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'delete' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "rename")) {
                if (!strcasecmp(attr->value, "true")) {
                    rename = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'rename' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "admin")) {
                if (!strcasecmp(attr->value, "true")) {
                    admin = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'admin' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else {
                printf("mdbtool: Unknown option: %s\n", attr->key);
                result = FALSE;
            }
        }
        
        attr = attr->next;
    }

    if (result) {
        if ((vs = MDBCreateValueStruct(h, NULL))) {
            result = MDBGrantObjectRights(object, trustee, read, write, 
                    delete, rename, admin, vs);

            if (!result) {
                printf("mdbtool: Could not grant access rights.\n");
            }
        } else {
            printf("ERROR: Could not allocate MDB resources.\n");
            result = FALSE;
        }
    }
    
    FreeDict(attrs);
    return result;
}

BOOL
GrantAttr(MDBHandle *h, char **argv, int argc, int next_arg)
{
    unsigned char *object = NULL;
    unsigned char *attrname = NULL;
    unsigned char *trustee = NULL;
    BOOL read = FALSE;
    BOOL write = FALSE;
    BOOL admin = FALSE;
    MDBValueStruct *vs = NULL;
    struct dict *attrs = NULL;
    struct dict *attr = NULL;
    BOOL result = TRUE;

    if (next_arg > (argc - 3)) {
        printf("Usage mdbtool grantattr <object> <attrname> <trustee> "
                "[opt1=val1 opt2=...]\n");
        return FALSE;
    }

    object = (unsigned char *) argv[next_arg++];
    attrname = (unsigned char *) argv[next_arg++];
    trustee = (unsigned char *) argv[next_arg++];

    /* Requires quotes or an escape char on the command line */
    if (!strcmp(attrname, "*")) {
	attrname = NULL;
    }

    attrs = GetDict(&argv[next_arg], argc - next_arg);
    attr = attrs;

    while (result && attr) {
        if (attr->key && attr->value) {
            if (!strcmp(attr->key, "read")) {
                if (!strcasecmp(attr->value, "true")) {
                    read = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'read' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "write")) {
                if (!strcasecmp(attr->value, "true")) {
                    write =  TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'write' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else if (!strcmp(attr->key, "admin")) {
                if (!strcasecmp(attr->value, "true")) {
                    admin = TRUE;
                } else if (strcasecmp(attr->value, "false")) {
                    printf("mdbtool: Option 'admin' invalid (must be 'true' "
                            "or 'false').\n");
                    result = FALSE;
                }
            } else {
                printf("mdbtool: Unknown option: %s\n", attr->key);
                result = FALSE;
            }
        }
        
        attr = attr->next;
    }

    if (result) {
        if ((vs = MDBCreateValueStruct(h, NULL))) {
            result = MDBGrantAttributeRights(object, attrname, trustee, read, 
                    write, admin, vs);

            if (!result) {
                printf("mdbtool: Could not grant access rights.\n");
            }
        } else {
            printf("ERROR: Could not allocate MDB resources.\n");
            result = FALSE;
        }
    }
    
    FreeDict(attrs);
    return result;
}

BOOL
GetHostIpAddr(void)
{
    struct in_addr addr;

    /* Maybe this shouldn't be in mdbtool, but bongosetup needs access to
       the XplGetHostIPAddress() function for consistency with msgapi */
    addr.s_addr = XplGetHostIPAddress();
    printf("%d.%d.%d.%d\n", addr.s_net, addr.s_host, addr.s_lh, addr.s_impno);

    return TRUE;
}

struct dict *
GetDict(char *list[], int items)
{
    struct dict *d;
    char *item;
    
    if (items == 0) {
        return NULL;
    }
    
    item = list[0];
    
    d = malloc(sizeof(struct dict));
    d->key = (unsigned char *) item;
    d->value = (unsigned char *) strchr(item, '=');
    *d->value = 0;
    d->value++;
    d->next = GetDict(&list[1], items - 1);
    
    return d;
}

void
FreeDict(struct dict *d)
{
    struct dict *tmp;
    
    while (d != NULL) {    
        tmp = d;
        d = d->next;
        free(tmp);
    }
}

/* broken vv */
void
DumpAttr(MDBHandle *handle, MDBValueStruct *vs, const unsigned char *object)
{
    /* MDBEnumStruct *e; */
    MDBValueStruct *v;
    const unsigned char *attr = NULL;
    
    v = MDBCreateValueStruct(handle, object);
    /*attr = MDBEnumerateAttributesEx(object, vs); FIXME: API */
    while(attr != NULL) {
        long l = MDBRead(object, attr, v);
        if (l != 0) {
            long i;
            for (i=0; i<l; i++) {
                printf("\t%s: %s\n", attr, v->Value[i]);
            }
        }
        /*attr = MDBEnumerateAttributesEx(object, vs);*/
    }
    
    MDBDestroyValueStruct(v);
}

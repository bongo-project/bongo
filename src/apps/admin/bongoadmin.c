/****************************************************************************
 * DONE:
 * Add user
 * Delete user (does not remove their mailbox though)
 * Delete mailing list
 * Delete mailing list member
 *
 * TODO:
 * modify users (i.e. change attributes, etc)
 * Verify all return codes
 * x.500 to MDB name conversion
 *
 * Farther down the road...
 * add rest of Mailing list management (add/modify)
 ****************************************************************************/
#include <config.h>
#include "bongoadmin.h"
#include <xpl.h>
#include <memmgr.h>
#include <bongoutil.h>
#include <errno.h>
#include <connio.h>

enum BongoAdminActions {
    BONGO_ADMIN_ADD,
    BONGO_ADMIN_MODIFY,
    BONGO_ADMIN_DELETE,
};

enum BongoAdminCommand {
    BONGO_ADMIN_USER,
    BONGO_ADMIN_LIST,
    BONGO_ADMIN_LIST_USER,
};

struct _BONGO_GLOBAL_VARIABLES {
    MDBHandle directoryHandle;

    struct {
        unsigned char name[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char password[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char context[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char givenname[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char surname[MDB_MAX_OBJECT_CHARS + 1];
    } user;
    
    struct {
        unsigned char name[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char context[MDB_MAX_OBJECT_CHARS + 1];
    } list;
    
    enum BongoAdminActions action;
    enum BongoAdminCommand command;

    struct {
        unsigned char x500Name[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char name[MDB_MAX_OBJECT_CHARS + 1];
        unsigned char password[MDB_MAX_OBJECT_CHARS + 1];
    } authentication;      
    BOOL verbose;
} BongoAdmin;

BOOL CheckRequired(void);

static void 
BongoAdminUsage(void)
{
    XplConsolePrintf("%s", NL);
    XplConsolePrintf("bongoadmin - Bongo Admin Utility.%s", NL);
    XplConsolePrintf("usage: bongoadmin <command> <options>%s", NL);
    XplConsolePrintf("Type \"bongoadmin help <command>\" for help with a specific command.%s%s", NL, NL);
    XplConsolePrintf("Supported commands are:%s", NL);
    XplConsolePrintf("  user                        Add/Delete/Modify a User.%s", NL);
    XplConsolePrintf("  list                        Add/Delete/Modify a Mailing List.%s", NL);
    XplConsolePrintf("  list-user                   Add/Delete/Modify a Mailing List member.%s", NL);
    XplConsolePrintf("  help                        Help%s", NL);
    XplConsolePrintf("  --verbose                   Display verbose progress messages.%s", NL);

    return;
}
static void 
BongoAdminListUsage(void)
{
    XplConsolePrintf("%s", NL);
    XplConsolePrintf("bongoadmin - Bongo Admin Utility.%s", NL);
    XplConsolePrintf("usage: bongoadmin list delete -u=<> -p=<> -L=<>%s", NL);
    XplConsolePrintf("usage: bongoadmin list delete-member -u=<> -p=<> -U=<> [ -L=<> | --fromall ]%s%s", NL, NL);
    XplConsolePrintf("  -U=<username>               The member to remove from the list.%s", NL);
    XplConsolePrintf("  -L=<listname>               The list to remove.%s", NL);
    XplConsolePrintf("  --from-all                  Remove the member from all lists (not implemented yet).%s", NL);
    XplConsolePrintf("  -p= | --pass=<text>         The password used for MDB authentication.%s", NL);
    XplConsolePrintf("  -u= | --user=<name>         A user name for MDB authentication.%s", NL);
    XplConsolePrintf("                                example: \\\\Tree\\\\Context\\\\Admin%s", NL);
    XplConsolePrintf("  --verbose                   Display verbose progress messages.%s", NL);
    return;
}

static void 
BongoAdminListUserUsage(void)
{
    XplConsolePrintf("%s", NL);
    XplConsolePrintf("bongoadmin - Bongo Admin Utility.%s", NL);
    XplConsolePrintf("usage: bongoadmin list-user delete -u=<> -p=<> -U=<> [ -L=<> | --fromall ]%s", NL);
    XplConsolePrintf("  -U=<username>               The member to remove from the list.%s%s", NL, NL);
    XplConsolePrintf("  -L=<listname>               The list to remove.%s", NL);
    XplConsolePrintf("  --from-all                  Remove the member from all lists (not implemented yet).%s", NL);
    XplConsolePrintf("  -p= | --pass=<text>         The password used for MDB authentication.%s", NL);
    XplConsolePrintf("  -u= | --user=<name>         A user name for MDB authentication.%s", NL);
    XplConsolePrintf("                                example: \\\\Tree\\\\Context\\\\Admin%s", NL);
    XplConsolePrintf("  --verbose                   Display verbose progress messages.%s", NL);
    return;
}

static void 
BongoAdminUserUsage(void)
{
    XplConsolePrintf("%s", NL);
    XplConsolePrintf("bongoadmin - Bongo Admin Utility.%s", NL);
    XplConsolePrintf("usage: bongoadmin user add -u=<> -p=<> -U=<> -P=<> -c=<> -s=<> [-g=<>]%s", NL);
    XplConsolePrintf("usage: bongoadmin user delete -u=<> -p=<> -U=<> -c=<>%s%s", NL, NL);
    XplConsolePrintf("  -U=<user>                   The username to use for the new user.%s", NL);
    XplConsolePrintf("  -P=<text>                   The password to use for the new user.%s", NL);
    XplConsolePrintf("  -c= | --context=<text>      The context where the user should be created.%s", NL);
    XplConsolePrintf("  -g= | --givenname=<text>    The Given Name of the new user.%s", NL);
    XplConsolePrintf("  -s= | --surname=<text>      The Surname of the new user.%s", NL);
    XplConsolePrintf("  -p= | --pass=<text>         The password used for MDB authentication.%s", NL);
    XplConsolePrintf("  -u= | --user=<name>         A user name for MDB authentication.%s", NL);
    XplConsolePrintf("                                example: \\\\Tree\\\\Context\\\\Admin%s", NL);
    XplConsolePrintf("  --verbose                   Display verbose progress messages.%s", NL);
	XplConsolePrintf("Examples:%s",NL);
	XplConsolePrintf("bongoadmin user add -u=\\\\Tree\\\\Context\\\\Admin -p=bongo -U=scott -P=villinski -s=Villinski -c=Context%s",NL);
	XplConsolePrintf("bongoadmin user delete -u=\\\\Tree\\\\Context\\\\Admin -p=bongo -U=scott -c=Context%s",NL);
    return;
}
BOOL
CheckRequired (void)
{
    /* this is where we check to make sure that the required parametes have been entered */
    BOOL passedCheck = TRUE;
    
    switch (BongoAdmin.command) {        
        case BONGO_ADMIN_USER: {
            /* User actions */
            switch (BongoAdmin.action) {
                case BONGO_ADMIN_ADD: {
                    /* user add requires: context, username, password, surname */
                    if (!BongoAdmin.user.name[0]) {
                        XplConsolePrintf("You must specify a username for the new user.%s", NL);
                        passedCheck = FALSE;
                    }
                    if (!BongoAdmin.user.password[0]) {
                        XplConsolePrintf("You must specify a password for the new user.%s", NL);
                        passedCheck = FALSE;
                    }
                    if (!BongoAdmin.user.context[0]) {
                        XplConsolePrintf("You must specify a context for the new user.%s", NL);
                        passedCheck = FALSE;
                    }
					if (!BongoAdmin.user.surname[0]) {
						XplConsolePrintf("You must specify a surname for the new user.%s",NL);
						passedCheck = FALSE;
					}
                    break;
                } case BONGO_ADMIN_DELETE: {
                    /* user delete requires: context, username */
                    if (!BongoAdmin.user.name[0]) {
                        XplConsolePrintf("You must specify the username to delete.%s", NL);
                        passedCheck = FALSE;
                    }
                    if (!BongoAdmin.user.context[0]) {
                        XplConsolePrintf("You must specify the context for the user.%s", NL);
                        passedCheck = FALSE;
                    }
                    break;
                } default :{
                    passedCheck = FALSE;
                    break;
                }
            }
            break;
        } case BONGO_ADMIN_LIST: {
            /* List actions */
            switch (BongoAdmin.action) {
                case BONGO_ADMIN_DELETE: {
                    /* List delete requires mailing list */
                    if (!BongoAdmin.list.name[0]) {
                        XplConsolePrintf("You must specify a Mailing List to delete.%s", NL);
                        passedCheck = FALSE;
                    }
                    break;
                } default : {
                    passedCheck = FALSE;
                    break;                
                }
            }
            break;
        } case BONGO_ADMIN_LIST_USER: {
            /* List-user actions */
            switch (BongoAdmin.action) {                
                case BONGO_ADMIN_DELETE: {
                    /* List-user delete requires user name and mailing list name */
                    if (!BongoAdmin.list.name[0]){
                        XplConsolePrintf("You must specify a Mailing List or all lists.%s", NL);
                        passedCheck = FALSE;
                    }                
                    if (!BongoAdmin.user.name[0]) {
                        XplConsolePrintf("You must specify the member to remove.%s", NL);
                        passedCheck = FALSE;
                    }
                    break;
                } default : {
                    passedCheck = FALSE;
                    break;
                }
            }
            break;
        } default:
            BongoAdminUsage();
    }
    return (passedCheck);
}

static void
PrintUsage(void)
{
    switch (BongoAdmin.command) {
        /* Print out the usage for the various commands */
        case BONGO_ADMIN_USER: {
            BongoAdminUserUsage();
            break;
        } case BONGO_ADMIN_LIST: {
            BongoAdminListUsage();
            break;
        } case BONGO_ADMIN_LIST_USER: {
            BongoAdminListUserUsage();
            break;
        } default: {
            BongoAdminUsage();
        }
    }
}

static void
CreateUser(MDBValueStruct *v, MDBValueStruct *data, MDBValueStruct *attribute)
{
    BOOL result = FALSE;
    if (BongoAdmin.verbose) {
        XplConsolePrintf("Checking to see if the context: %s already exists.%s", BongoAdmin.user.context, NL);
    }
    if (MDBIsObject(BongoAdmin.user.context, v) == TRUE) {
        MDBSetValueStructContext(BongoAdmin.user.context, v);
        attribute = MDBShareContext(v);
        data = MDBShareContext(v);
                
        if (BongoAdmin.verbose) {            
            XplConsolePrintf("Checking to see if user: %s already exists.%s", BongoAdmin.user.name, NL);
        }
        if (MDBIsObject(BongoAdmin.user.name, v) == FALSE) {
            /* Check to see if the user already exists */
            if (BongoAdmin.verbose) {
                XplConsolePrintf("Creating user: %s%s", BongoAdmin.user.name, NL);
            }
            /* Create the user */
            result = MDBCreateObject(BongoAdmin.user.name, "User", attribute, data, v);
            if (!result) {
                XplConsolePrintf("Failed to create user: %s%s", BongoAdmin.user.name, NL);
            } else {
                XplConsolePrintf("Successfully created new user: \\%s\\%s%s", BongoAdmin.user.context, BongoAdmin.user.name, NL);
            }
                        
            if (BongoAdmin.user.givenname[0]) {
                /* Add Given Name (if passed) */
                if (BongoAdmin.verbose) {
                    XplConsolePrintf("Adding Given Name: %s%s", BongoAdmin.user.givenname, NL);
                }
                result = MDBAdd(BongoAdmin.user.name, "Given Name", BongoAdmin.user.givenname, v);
                if (!result) {
                        XplConsolePrintf("Failed to set Given Name for user: %s%s", BongoAdmin.user.name, NL);
                }
            }
                        
            if (BongoAdmin.user.surname[0]) {
                /* Add Surname (if passed) */
                if (BongoAdmin.verbose) {
                    XplConsolePrintf("Adding Surname: %s%s", BongoAdmin.user.surname, NL);
                }
                result = MDBAdd(BongoAdmin.user.name, "Surname", BongoAdmin.user.surname, v);
                if (!result) {
                        XplConsolePrintf("Failed to set Surname for user: %s%s", BongoAdmin.user.name, NL);
                }
            }
                        
            if (BongoAdmin.verbose) {
                XplConsolePrintf("Setting password for: %s%s", BongoAdmin.user.name, NL);
            }
            /* Set the new user's password */
            result = MDBChangePasswordEx(BongoAdmin.user.name, NULL, BongoAdmin.user.password, v);
            if (!result) {
                    XplConsolePrintf("Failed to set password for user: %s%s", BongoAdmin.user.name, NL);
            }
        } else {
            XplConsolePrintf("The user: %s already exists in the context: %s.%s", BongoAdmin.user.name, BongoAdmin.user.context, NL);
        }
    } else {
        XplConsolePrintf("The context: %s does not exist, please create the context before proceeding.%s", BongoAdmin.user.context, NL);
    }
}

static void
DeleteUser (MDBValueStruct *v)
{
    BOOL result = FALSE;
    if (BongoAdmin.verbose) {
        XplConsolePrintf("Checking to see if the context: %s exists.%s", BongoAdmin.user.context, NL);
    }
    if (MDBIsObject(BongoAdmin.user.context, v) == TRUE) {
        MDBSetValueStructContext(BongoAdmin.user.context, v);
        
        if (BongoAdmin.verbose) {
            XplConsolePrintf("Checking to see if user: %s  exists.%s", BongoAdmin.user.name, NL);
        }
        if (MDBIsObject(BongoAdmin.user.name, v) == TRUE) {
            /* Check to see if the user exists */            
            if (BongoAdmin.verbose) {
                XplConsolePrintf("Deleting user: %s%s", BongoAdmin.user.name, NL);
            }
            /* Delete the user */
            result = MDBDeleteObject(BongoAdmin.user.name, FALSE, v);
            if (!result) {
                XplConsolePrintf("Failed to delete user: %s%s", BongoAdmin.user.name, NL);
            } else {
                XplConsolePrintf("Successfully deleted user: \\%s\\%s%s", BongoAdmin.user.context, BongoAdmin.user.name, NL);
            }
        } else {
            XplConsolePrintf("The user: %s does not exist in the context: %s.%s", BongoAdmin.user.name, BongoAdmin.user.context, NL);
        }
    } else {
        XplConsolePrintf("The context: %s does not exist.%s", BongoAdmin.user.context, NL);
    }
}


static void
DeleteList (MDBValueStruct *v)
{
    BOOL result = FALSE;    
    if (BongoAdmin.verbose) {
        XplConsolePrintf("Checking to see if the Mailing List container: %s exists.%s", BongoAdmin.list.context, NL);
    }
    if (MDBIsObject(BongoAdmin.list.context, v) == TRUE) {
        MDBSetValueStructContext(BongoAdmin.list.context, v);
        
        if (BongoAdmin.verbose) {
            XplConsolePrintf("Checking to see if the Mailing List: %s  exists.%s", BongoAdmin.list.name, NL);
        }
        if (MDBIsObject(BongoAdmin.list.name, v) == TRUE) {
            /* Check to see if the list exists */            
            if (BongoAdmin.verbose) {
                XplConsolePrintf("Deleting Mailing List: %s%s", BongoAdmin.list.name, NL);
            }
            /* Delete the list */
            /* Besure to set the Recurse Boolean to TRUE - to delete all sub objects */
            result = MDBDeleteObject(BongoAdmin.list.name, TRUE, v);
            if (!result) {
                XplConsolePrintf("Failed to delete Mailing List: %s%s", BongoAdmin.list.name, NL);
            } else {
                XplConsolePrintf("Successfully deleted Mailing List: \\%s%s%s", BongoAdmin.list.context, BongoAdmin.list.name, NL);
            }
        } else {
            XplConsolePrintf("The Mailing List: %s does not exist.%s", BongoAdmin.list.name, NL);
        }
    } else {
        XplConsolePrintf("The Mailing List container: %s does not exist.%s", BongoAdmin.list.context, NL);
    }
}

static void
DeleteListUser (MDBValueStruct *v)
{   
    BOOL result = FALSE;    
    if (BongoAdmin.verbose) {
        XplConsolePrintf("Checking to see if the Mailing List: %s exists.%s", BongoAdmin.list.name, NL);
    }
    if (MDBIsObject(strcat(BongoAdmin.list.context, BongoAdmin.list.name), v) == TRUE) {
        MDBSetValueStructContext(BongoAdmin.list.context, v);
        
        if (BongoAdmin.verbose) {
            XplConsolePrintf("Checking to see if the address: %s is a member of the Mailing List: %s  exists.%s", BongoAdmin.list.name, BongoAdmin.user.name, NL);
        }
        if (MDBIsObject(BongoAdmin.user.name, v) == TRUE) {
            /* Check to see if the user is a member of the Mailing List exists */            
            if (BongoAdmin.verbose) {
                XplConsolePrintf("Deleting Mailing List member: %s%s", BongoAdmin.user.name, NL);
            }
            /* Delete the user */
            result = MDBDeleteObject(BongoAdmin.user.name, FALSE, v);
            if (!result) {
                XplConsolePrintf("Failed to delete Mailing List member: %s%s", BongoAdmin.user.name, NL);
            } else {
                XplConsolePrintf("Successfully deleted member: %s from Mailing List: %s%s", BongoAdmin.user.name, BongoAdmin.list.name, NL);
            }
        } else {
            XplConsolePrintf("The address: %s is not a member of Mailing List: %s.%s", BongoAdmin.user.name, BongoAdmin.list.name, NL);
        }
    } else {
        XplConsolePrintf("The Mailing List: %s does not exist.%s", BongoAdmin.list.name, NL);
    }
}

int
main(int argc, char *argv[])
{
    int i = 0;
    BOOL result = TRUE;
    MDBValueStruct *v;
    MDBValueStruct *attribute = NULL;
    MDBValueStruct *data = NULL;
    
    /* initialize variables */
    BongoAdmin.command = -1;
    BongoAdmin.action = -1;
    BongoAdmin.user.name[0] = '\0';
    BongoAdmin.user.context[0] = '\0';
    BongoAdmin.user.password[0] = '\0';
    BongoAdmin.user.givenname[0] = '\0';
    BongoAdmin.user.surname[0] = '\0';
    BongoAdmin.list.name[0] = '\0';
    BongoAdmin.verbose = FALSE;
    strcpy(BongoAdmin.list.context, MAILING_LIST_CONTEXT);

    
    result = MemoryManagerOpen("Bongo Admin");

    if (!result) {
        XplConsolePrintf("Failed to initialize the memory pool library.%s", NL);
        return(-1);
    }
    
    for (i = 1; i < argc; i++) {
        /* Read the command line arguments */        
        if ((XplStrCaseCmp(argv[i], "--help") == 0) 
                || (XplStrCaseCmp(argv[i], "help") == 0) 
                || (XplStrCaseCmp(argv[i], "-?") == 0)) {
                    /* Check to see if help was specified */
                    result = FALSE;
        
        } else if (XplStrCaseCmp(argv[i], "user") == 0) {
            BongoAdmin.command = BONGO_ADMIN_USER;                            
        } else if (XplStrCaseCmp(argv[i], "list") == 0) {
            BongoAdmin.command = BONGO_ADMIN_LIST;
        } else if (XplStrCaseCmp(argv[i], "list-user") == 0) {
            BongoAdmin.command = BONGO_ADMIN_LIST_USER;
        } else if (XplStrCaseCmp(argv[i], "add") == 0) {
            BongoAdmin.action = BONGO_ADMIN_ADD;
        } else if (XplStrCaseCmp(argv[i], "modify") == 0) {
            BongoAdmin.action = BONGO_ADMIN_MODIFY;
        } else if (XplStrCaseCmp(argv[i], "delete") == 0) {
            BongoAdmin.action = BONGO_ADMIN_DELETE;
        } else if (strncmp(argv[i], "-u=", 3) == 0) {
            /* Read the username to Authenticate to Bongo */
            strcpy(BongoAdmin.authentication.name, argv[i] + 3);
        } else if (strncmp(argv[i], "--user=", 7) == 0) {
            /* Read the username to Authenticate to Bongo */
            strcpy(BongoAdmin.authentication.name, argv[i] + 7);
                    
        } else if (strncmp(argv[i], "-p=", 3) == 0) {
            /* Read the password to authenticate to Bongo */
            strcpy(BongoAdmin.authentication.password, argv[i] + 3);
        } else if (strncmp(argv[i], "--pass=", 7) == 0) {
            /* Read the password to authenticate to Bongo */
            strcpy(BongoAdmin.authentication.password, argv[i] + 7);
                
        } else if (strncmp(argv[i], "--verbose", 9) == 0) {
            /* Check to see if verbose was enabled */
            BongoAdmin.verbose = TRUE;        
                
        } else if (strncmp(argv[i], "-U=", 3) == 0) {
            /* Read the username */
            strcpy(BongoAdmin.user.name, argv[i] + 3);
                    
        } else if (strncmp(argv[i], "-P=", 3) == 0) {
            /* read the password for the new user */
            strcpy(BongoAdmin.user.password, argv[i] + 3);
                    
        } else if (strncmp(argv[i], "-g=", 3) == 0) {
            /* read the Given Name for the new user */
            strcpy(BongoAdmin.user.givenname, argv[i] + 3);
        } else if (strncmp(argv[i], "--givenname=", 12) == 0) {
            /* read the Given Name for the new user */
            strcpy(BongoAdmin.user.givenname, argv[i] + 12);
                    
        } else if (strncmp(argv[i], "-s=", 3) == 0) {
            /* Read the Surname for the new user */
            strcpy(BongoAdmin.user.surname, argv[i] + 3);
        } else if (strncmp(argv[i], "--surname=", 10) == 0) {
            /* Read the Surname for the new user */
            strcpy(BongoAdmin.user.surname, argv[i] + 10);
        
        } else if (strncmp(argv[i], "-c=", 3) == 0) {
            /* Read the Context */
            strcpy(BongoAdmin.user.context, argv[i] + 3);
        } else if (strncmp(argv[i], "--context=", 11) == 0) {
            /* Read the Context */
            strcpy(BongoAdmin.user.context, argv[i] + 11);
                
        } else if (strncmp(argv[i], "-L=", 3) == 0) {
            /* Read the listname */
            strcpy(BongoAdmin.list.name, argv[i] + 3);
        }
    }
        
    if ((!result) || (argc <= 2)) {
        /* Print out the Bongo Admin help if no arguments or --help was specified */
        PrintUsage();
        return(0);
    }

    if (!CheckRequired()) {
        /* Check to make certain that we have the required data based on the action to perform */
        /* If we fail - print out the usage for the specified command */
        PrintUsage();
        return(0);        
    }
    
    /* If we get there - that means thins are looking up! */
        
    if (BongoAdmin.verbose) {
        XplConsolePrintf("Initializing MDB.%s", NL);
    }
    if (!MDBInit()) {
        /* Init MDB */
        XplConsolePrintf("bongoadmin: Exiting\n.");
        return 1;
    }

    if (BongoAdmin.verbose) {
        XplConsolePrintf("Authenticating to MDB.%s", NL);
    }
    if (BongoAdmin.authentication.name[0]) {
        /* Authenticate to MDB */
        if (BongoAdmin.verbose) {
            XplConsolePrintf("Authenticating as: %s%s", BongoAdmin.authentication.name, NL);
        }
        BongoAdmin.directoryHandle = MDBAuthenticate("Bongo", BongoAdmin.authentication.name, BongoAdmin.authentication.password);
    } else {
        /* if no username is provided, we'll auth as public user - mileage may very */
        if (BongoAdmin.verbose) {
            XplConsolePrintf("Performing public authentication.%s", NL);
        }
        BongoAdmin.directoryHandle = MDBAuthenticate("Bongo", NULL, NULL);
    }
        
    if (BongoAdmin.directoryHandle) {
        /* Verify that we have a directoryHandle to talk to MDB */
        /* get the context in MDB */
        v = MDBCreateValueStruct(BongoAdmin.directoryHandle, NULL);  
        switch (BongoAdmin.command) {
            case BONGO_ADMIN_USER: {
                switch (BongoAdmin.action) {
                    case BONGO_ADMIN_ADD: {
                        /* call CreateUser() */
                        CreateUser(v, data, attribute);
                        break;
                    } case BONGO_ADMIN_DELETE: {
                        /* call DeleteUser() */
                        DeleteUser(v);
                        break;
                    } default: {
                        break;
                    }
                }
                break;
            } case BONGO_ADMIN_LIST: {
                switch (BongoAdmin.action) {
                    case BONGO_ADMIN_DELETE: {
                        /* call DeleteList() */
                        DeleteList(v);
                        break;
                    } default: {
                        break;
                    }
                }
                break;
            } case BONGO_ADMIN_LIST_USER: {
                switch (BongoAdmin.action) {
                    case BONGO_ADMIN_DELETE: {
                        /* call DeleteListUser() */
                        DeleteListUser(v);
                        break;
                    } default: {
                        break;
                    }
                }
                break;
            } default: {
                break;
            }
        }
    } else if (BongoAdmin.verbose) {
        XplConsolePrintf("Could not authenticate to MDB.%s", NL);
    }
    
    if (BongoAdmin.verbose) {
        XplConsolePrintf("Logging out of MDB.%s", NL);
    }

    if (!MDBRelease(BongoAdmin.directoryHandle)) {
        XplConsolePrintf("Failed to release MDBHandle.%s", NL);
    }

    MemoryManagerClose("Bongo Admin");
    
    return 0;
}

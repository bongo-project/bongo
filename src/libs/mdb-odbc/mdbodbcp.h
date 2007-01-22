#ifndef MDBODBCP_H
#define MDBODBCP_H

#define MDB_ODBC_ROOT_ID        -1
#define VALUE_ALLOC_SIZE        20
#define MDB_ODBC_NAME_SIZE      64
#define MDB_ODBC_PART_SIZE      255

typedef struct {
    /* Semi-public; known to the main MDB library */
    unsigned char Description[128];
    struct _MDBInterfaceStruct *Interface;

    /* Private */
    signed long object;
    SQLHDBC connection;
} MDBodbcContextStruct;

typedef MDBodbcContextStruct *MDBHandle;

typedef struct {
    /* Public part, exposed to allow simpler code */
    unsigned char **Value;
    unsigned long Used;
    unsigned long ErrNo;
    struct _MDBInterfaceStruct *Interface;

    /* Private part, not exposed in public header */
    unsigned long Allocated;
    MDBodbcContextStruct *handle;

    signed long statementCount;

    /*
        The base pointer will be used whenever the Base DN ID is needed.  This is
        a pointer so that it can be set to the address of another value struct when
        MDBShareContext() is used.
    */
    struct {
        SQLINTEGER *id;
        SQLINTEGER value;

        SQLINTEGER *class;
        SQLINTEGER classValue;

        BOOL *read;
        BOOL readValue;
        BOOL *write;
        BOOL writeValue;
        BOOL *delete;
        BOOL deleteValue;
        BOOL *rename;
        BOOL renameValue;
    } base;

    struct {
        unsigned char dn[255 + 1];
        SQLINTEGER id;
        SQLINTEGER class;

        BOOL read;
        BOOL write;
        BOOL delete;
        BOOL rename;
        unsigned long time;
    } cache;

    struct {
        SQLHSTMT asArray[0];
        SQLHSTMT getTreeInfo;
        SQLHSTMT setTreeInfo;
        SQLHSTMT getRootRights;
        SQLHSTMT getRightsProviders;
        SQLHSTMT getRights;
        SQLHSTMT setRights;
        SQLHSTMT delRightsByProviderAndObject;
        SQLHSTMT delRightsByProvider;
        SQLHSTMT delRightsByObject;
        SQLHSTMT delRightsByParent;
        SQLHSTMT delRightsByChild;
        SQLHSTMT getObjectID;
        SQLHSTMT getAttrByName;
        SQLHSTMT getClassByName;
        SQLHSTMT getObjectAttrInfo;
        SQLHSTMT getParentInfo;
        SQLHSTMT getObjectClass;
        SQLHSTMT getObjectClassName;
        SQLHSTMT verifyContainment;
        SQLHSTMT listContainment;
        SQLHSTMT createObject;
        SQLHSTMT createAttribute;
        SQLHSTMT createClass;
        SQLHSTMT createSuperClass;
        SQLHSTMT deleteObjectValues;
        SQLHSTMT deleteObjectRights;
        SQLHSTMT deleteObject;
        SQLHSTMT copySuperClass;
        SQLHSTMT createContainment;
        SQLHSTMT createMandatory;
        SQLHSTMT getMandatoryByName;
        SQLHSTMT createOptional;
        SQLHSTMT createNaming;
        SQLHSTMT copyContainment;
        SQLHSTMT copyMandatory;
        SQLHSTMT copyOptional;
        SQLHSTMT copyNaming;
        SQLHSTMT clearAttribute;
        SQLHSTMT addDNValue;
        SQLHSTMT addStringValue;
        SQLHSTMT addBoolValue;
        SQLHSTMT moveObject;
        SQLHSTMT renameObject;
        SQLHSTMT verifyPassword;
        SQLHSTMT setPassword;
        SQLHSTMT deleteAttr;
        SQLHSTMT deleteAttrFromMandatory;
        SQLHSTMT deleteAttrFromOptional;
        SQLHSTMT deleteAttrFromNaming;
        SQLHSTMT deleteAttrFromValues;
        SQLHSTMT deleteAttrFromRights;
        SQLHSTMT deleteClassFromContainment;
        SQLHSTMT deleteClassFromMandatory;
        SQLHSTMT deleteClassFromOptional;
        SQLHSTMT deleteClassFromNaming;
        SQLHSTMT deleteClass;
        SQLHSTMT readValue;
        SQLHSTMT getChildren;
        SQLHSTMT getChildrenByClass;
        SQLHSTMT getChildrenIDs;
        SQLHSTMT hasChildren;
    } stmts;

    union {
        struct {
            SQLINTEGER read;
            SQLINTEGER write;
            SQLINTEGER delete;
            SQLINTEGER rename;
        } getRootRights;

        struct {
            SQLINTEGER trustee;
            SQLINTEGER provider;
        } getRightsProviders;

        /* Used by getRights and setRights */
        struct {
            SQLINTEGER object;
            SQLINTEGER trustee;
            SQLINTEGER provider;
            SQLINTEGER parent;
            SQLINTEGER read;
            SQLINTEGER write;
            SQLINTEGER delete;
            SQLINTEGER rename;
        } setGetRights;

        struct {
            SQLINTEGER provider;
            SQLINTEGER object;
        } delRightsByProviderAndObject;

        struct {
            SQLINTEGER provider;
        } delRightsByProvider;

        struct {
            SQLINTEGER object;
            SQLINTEGER trustee;
        } delRightsByObject;

        struct {
            SQLINTEGER trustee;
        } delRightsByParent;

        struct {
            SQLINTEGER provider;
            SQLINTEGER trustee;
        } delRightsByChild;

        struct {
            unsigned char object[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER objectLength;
            SQLINTEGER id;
            SQLINTEGER class;
            SQLINTEGER read;
            SQLINTEGER write;
            SQLINTEGER delete;
            SQLINTEGER rename;
        } getObjectID;

        struct {
            unsigned char attribute[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER attributeLength;
            SQLINTEGER id;
        } getAttrByName;

        struct {
            SQLINTEGER attribute;
        } deleteAttr;

        struct {
            SQLINTEGER object;
            unsigned char attribute[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER attributeLength;
            SQLINTEGER id;
            SQLINTEGER single;
            SQLINTEGER type;
        } getObjectAttrInfo;

        struct {
            SQLINTEGER object;
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
        } getParentInfo;

        struct {
            SQLINTEGER object;
            SQLINTEGER class;
        } getObjectClass;

        struct {
            SQLINTEGER object;
            unsigned char class[MDB_ODBC_NAME_SIZE + 1];
        } getObjectClassName;

        struct {
            SQLINTEGER object;
            SQLINTEGER child;
        } hasChildren;

        struct {
            SQLINTEGER object;
            SQLINTEGER class;
        } verifyContainment;

        struct {
            SQLINTEGER object;
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
        } listContainment;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER parent;
            SQLINTEGER parentClass;
            unsigned char class[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER classLength;
        } createObject;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER type;
            SQLINTEGER single;
            SQLINTEGER public;
        } createAttribute;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER container;
        } createClass;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } getClassByName;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } createSuperClass;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } copySuperClass;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } createContainment;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } createMandatory;

        struct {
            unsigned char class[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER classLength;
        } getMandatoryByName;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } createOptional;

        struct {
            unsigned char name[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER nameLength;
            SQLINTEGER class;
        } createNaming;

        struct {
            SQLINTEGER object;
        } deleteObjectValues;

        struct {
            SQLINTEGER object;
        } deleteObjectRights;

        struct {
            SQLINTEGER object;
        } deleteObject;

        struct {
            SQLINTEGER class;
        } copyContainment;

        struct {
            SQLINTEGER class;
        } copyMandatory;

        struct {
            SQLINTEGER class;
        } copyOptional;

        struct {
            SQLINTEGER class;
        } copyNaming;

        struct {
            SQLINTEGER object;
            SQLINTEGER attribute;
        } clearAttribute;

        struct {
            SQLINTEGER owner;
            SQLINTEGER attribute;
            SQLINTEGER value;
        } addDNValue;

        struct {
            SQLINTEGER owner;
            SQLINTEGER attribute;
            unsigned char value[MDB_ODBC_PART_SIZE + 1];
            SQLINTEGER valueLength;
            SQLINTEGER complete;
        } addStringValue;

        struct {
            SQLINTEGER owner;
            SQLINTEGER attribute;
            SQLINTEGER value;
        } addBoolValue;

        struct {
            SQLINTEGER object;
            SQLINTEGER parent;
        } moveObject;

        struct {
            SQLINTEGER object;
            unsigned char name[MDB_ODBC_PART_SIZE + 1];
            SQLINTEGER nameLength;
        } renameObject;

        struct {
            SQLINTEGER object;
            unsigned char pass[32 + 1];
            SQLINTEGER passLength;
        } verifyPassword;

        struct {
            SQLINTEGER object;
            unsigned char pass[32 + 1];
            SQLINTEGER passLength;
        } setPassword;

        struct {
            SQLINTEGER attribute;
        } deleteAttrFromMandatory;

        struct {
            SQLINTEGER attribute;
        } deleteAttrFromOptional;

        struct {
            SQLINTEGER attribute;
        } deleteAttrFromNaming;

        struct {
            SQLINTEGER attribute;
        } deleteAttrFromValues;

        struct {
            SQLINTEGER attribute;
        } deleteAttrFromRights;

        struct {
            SQLINTEGER class;
        } deleteClassFromContainment;

        struct {
            SQLINTEGER class;
        } deleteClassFromMandatory;

        struct {
            SQLINTEGER class;
        } deleteClassFromOptional;

        struct {
            SQLINTEGER class;
        } deleteClassFromNaming;

        struct {
            SQLINTEGER class;
        } deleteClass;

        struct {
            SQLINTEGER object;
            SQLINTEGER dn;
            SQLINTEGER bool;
            SQLINTEGER complete;
            SQLINTEGER type;
            unsigned char attribute[MDB_ODBC_NAME_SIZE + 1];
            unsigned long attributeLength;
            unsigned char value[MDB_ODBC_PART_SIZE + 1];
        } readValue;

        /* getChildren is also used by getChildrenByClass */
        struct {
            SQLINTEGER object;
            SQLINTEGER container;
            unsigned char class[MDB_ODBC_NAME_SIZE + 1];
            unsigned long classLength;
            unsigned char child[MDB_ODBC_NAME_SIZE + 1];
            SQLINTEGER allContainers;
        } getChildren;

        struct {
            SQLINTEGER object;
            SQLINTEGER child;
        } getChildrenIDs;
    } params;
} MDBValueStruct;

typedef struct {
    char *sql;
    SQLHSTMT stmt;

    /* This buffer should be large enough for any enum response */
    unsigned char result[MDB_ODBC_PART_SIZE + 1];

    /* Used for returning DN results where only the name changes from response to response */
    unsigned char dn[MDB_ODBC_PART_SIZE + 1];
    unsigned char class[MDB_ODBC_PART_SIZE + 1];
    SQLINTEGER classlen;
    unsigned char *name;

    SQLINTEGER object;

    /* These are all included as possible responses */
    SQLINTEGER id;
    SQLINTEGER bool;
    SQLINTEGER complete;
    SQLINTEGER type;
    SQLINTEGER container;
    SQLINTEGER allContainers;

    /* This should be used if MDBReadEx() reads a value alrger than MDB_ODBC_PART_SIZE */
    unsigned char *largeResult;
} MDBEnumStruct;

/*
    Do as much static setup as can be done here.  Some things, such as setting the
    admin password, or creating the tree entry will have to be done seperately since
    they are dynamic.

    We attempt to create some of the tables twice.  This is because mysql does not support
    SERIAL.  If we fail to create a table using SERIAL then we will try again with
    INTEGER AUTO_INCREMENT UNIQUE, which as far as this driver is concerned is the same thing.

    The last line creates the root object.  It can not be created with the regular
    creation statement for objects because it has no parent object.
*/
unsigned char *MDBodbcSQLPrepare[] = {
    "CREATE TABLE treeinfo"                                 \
    "("                                                     \
        "name               varchar(64),"                   \
        "defaultContainer   INTEGER"                        \
    ");",
    "CREATE TABLE objects"                                  \
    "("                                                     \
        "id                 SERIAL,"                        \
        "name               varchar(64),"                   \
        "class              INTEGER,"                       \
        "parent             INTEGER,"                       \
        "pass               char(32)"                       \
    ");",
    "CREATE TABLE objects"                                  \
    "("                                                     \
        "id                 INTEGER AUTO_INCREMENT UNIQUE," \
        "name               varchar(64),"                   \
        "class              INTEGER,"                       \
        "parent             INTEGER,"                       \
        "pass               char(32)"                       \
    ");",
    "CREATE TABLE classes"                                  \
    "("                                                     \
        "id                 SERIAL,"                        \
        "name               varchar(64) UNIQUE,"            \
        "container          INTEGER"                        \
    ");",
    "CREATE TABLE classes"                                  \
    "("                                                     \
        "id                 INTEGER AUTO_INCREMENT UNIQUE," \
        "name               varchar(64) UNIQUE,"            \
        "container          INTEGER"                        \
    ");",
    "CREATE TABLE superclasses"                             \
    "("                                                     \
        "subclass           INTEGER,"                       \
        "superclass         INTEGER"                        \
    ");",
    "CREATE TABLE containment"                              \
    "("                                                     \
        "container          INTEGER,"                       \
        "leaf               INTEGER"                        \
    ");",
    "CREATE TABLE attributes"                               \
    "("                                                     \
        "id                 SERIAL,"                        \
        "name               varchar(64),"                   \
        "type               INTEGER,"                       \
        "single             INTEGER,"                       \
        "public             INTEGER"                        \
    ");",
    "CREATE TABLE attributes"                               \
    "("                                                     \
        "id                 INTEGER AUTO_INCREMENT UNIQUE," \
        "name               varchar(64),"                   \
        "type               INTEGER,"                       \
        "single             INTEGER,"                       \
        "public             INTEGER"                        \
    ");",
    "CREATE TABLE mandatory"                                \
    "("                                                     \
        "attribute          INTEGER,"                       \
        "class              INTEGER"                        \
    ");",
    "CREATE TABLE optional"                                 \
    "("                                                     \
        "attribute          INTEGER,"                       \
        "class              INTEGER"                        \
    ");",
    "CREATE TABLE naming"                                   \
    "("                                                     \
        "attribute          INTEGER,"                       \
        "class              INTEGER"                        \
    ");",
    "CREATE TABLE data"                                     \
    "("                                                     \
        "id                 SERIAL,"                        \
        "object             INTEGER,"                       \
        "attribute          INTEGER,"                       \
        "value              VARCHAR(255)"                   \
                            " DEFAULT ' ',"                 \
        "dnvalue            INTEGER DEFAULT -1,"            \
        "boolvalue          INTEGER DEFAULT -1,"            \
        "complete           INTEGER DEFAULT -1"             \
    ");",
    "CREATE TABLE data"                                     \
    "("                                                     \
        "id                 INTEGER AUTO_INCREMENT UNIQUE," \
        "object             INTEGER,"                       \
        "attribute          INTEGER,"                       \
        "value              VARCHAR(255)"                   \
                            " DEFAULT ' ',"                 \
        "dnvalue            INTEGER DEFAULT -1,"            \
        "boolvalue          INTEGER DEFAULT -1,"            \
        "complete           INTEGER DEFAULT -1"             \
    ");",
    "CREATE TABLE rights"                                   \
    "("                                                     \
        "object             INTEGER DEFAULT -1,"            \
        "trustee            INTEGER DEFAULT -1,"            \
        "provider           INTEGER DEFAULT -1,"            \
        "r                  INTEGER DEFAULT -1,"            \
        "w                  INTEGER DEFAULT -1,"            \
        "d                  INTEGER DEFAULT -1,"            \
        "n                  INTEGER DEFAULT -1,"            \
        "parent             INTEGER DEFAULT 0"              \
    ");",

    "CREATE INDEX objects_index ON objects (id, parent, class, name);",
    "CREATE INDEX classes_index ON classes (id, name);",
    "CREATE INDEX superclasses_index ON superclasses (subclass);",
    "CREATE INDEX containment_index ON containment (container, leaf);",
    "CREATE INDEX attributes_index ON attributes (id, name);",
    "CREATE INDEX mandatory_index ON mandatory (class);",
    "CREATE INDEX optional_index ON optional (class);",
    "CREATE INDEX naming_index ON naming (class);",
    "CREATE INDEX values_index ON data (object, attribute);",
    "CREATE INDEX rights_index ON rights (object, trustee);",
    NULL
};

unsigned char *MDBodbcSQLCreateRoot[] = {
    "DELETE FROM objects;",
    "INSERT INTO objects (id, name, parent, class)"                             \
    " SELECT -1, ?, -1, id FROM classes WHERE UCASE(name)=UCASE('Top');",
    NULL
};

/*
    Set the rights to be such that anything is allowed by anyone while the setup
    is being run.  This will be changed after setup is complete.
*/
unsigned char *MDBodbcSQLSetPrepareRights[] = {
    "INSERT INTO objects (id, name, parent, class)"                             \
                " VALUES (-1, ?,    -1,     -1);",
    "INSERT INTO rights (object, trustee, r, w, d, n)"                          \
                " VALUES(-1,     -1,      1, 1, 1, 1);",
    NULL
};

/*
    Removes the temporary rights used during setup, and set the default rights
    which are none.  Also provide full rights to the root to the administrators
    group, and to admin.
*/
unsigned char *MDBodbcSQLSetInitialRights[] = {
    "DELETE FROM rights;",
    "INSERT INTO rights (object) VALUES (-1);",
    "INSERT INTO rights (object, provider, trustee, r, w, d, n)"                \
        " SELECT -1, objects.id, objects.id, 1, 1, 1, 1"                        \
        " FROM objects WHERE UCASE(objects.name)=UCASE('Administrators') OR"    \
        " UCASE(objects.name)=UCASE('Admin');",
    NULL
};

#define REMOVE_TABLES "DROP TABLE treeinfo; DROP TABLE objects;"                \
    "DROP TABLE classes;DROP TABLE superclasses; DROP TABLE containment;"       \
    "DROP TABLE attributes;DROP TABLE mandatory; DROP TABLE optional;"          \
    "DROP TABLE data;DROP TABLE rights; DROP TABLE naming;"

#define SQL_GET_TREE_INFO                   "SELECT name, defaultContainer FROM treeinfo LIMIT 1;"
#define SQL_SET_TREE_INFO                   "INSERT INTO treeinfo (name, defaultContainer) VALUES (?, ?);"
#define SQL_GET_ROOT_RIGHTS                 "SELECT r, w, d, n FROM rights WHERE trustee=? AND object=-1;"
#define SQL_DEL_RIGHTS_BY_PROVIDER_AND_OBJ  "DELETE FROM rights WHERE provider=? AND object=?;"
#define SQL_DEL_RIGHTS_BY_OBJECT            "DELETE FROM rights WHERE object=? AND trustee=?;"
#define SQL_GET_RIGHTS_PROVIDERS            "SELECT provider FROM rights WHERE trustee=? AND parent=1;"
#define SQL_DEL_RIGHTS_BY_CHILD             "DELETE FROM rights WHERE provider=? AND trustee=? AND parent=1;"
#define SQL_DEL_RIGHTS_BY_PROVIDER          "DELETE FROM rights WHERE provider=? AND provider!=trustee;"

#define SQL_SET_RIGHTS                      "INSERT INTO rights (object, trustee, provider, parent, r, w, d, n) VALUES (?, ?, ?, ?, ?, ?, ?, ?);"
#define SQL_GET_RIGHTS                      "SELECT provider, object, r, w, d, n FROM rights WHERE trustee=?;"

#define SQL_GET_OBJECT_ID                   "SELECT objects.id, objects.class, rights.r, rights.w, rights.d, rights.n FROM objects, rights"     \
                                                " WHERE UCASE(objects.name)=UCASE(?) AND objects.parent=? AND"                                  \
                                                " ((rights.object=objects.id AND rights.trustee=?) OR (rights.object=-1 AND rights.trustee=-1))"\
                                                " ORDER BY rights.trustee DESC LIMIT 1;"
#define SQL_GET_ATTRIBUTE_BY_NAME           "SELECT id FROM attributes WHERE UCASE(name)=UCASE(?);"
#define SQL_GET_CLASS_BY_NAME               "SELECT id FROM classes WHERE UCASE(name)=UCASE(?);"

#define SQL_GET_CHILDREN                    "SELECT objects.name, classes.container FROM objects, classes WHERE objects.parent=? AND objects.id>0 AND objects.class=classes.id;"
#define SQL_GET_CHILDREN_BY_CLASS           "SELECT objects.name, classes.container FROM objects, classes WHERE objects.parent=? AND objects.id>0 AND objects.class=classes.id" \
                                                " AND (UCASE(classes.name)=UCASE(?) OR classes.container=?);"
#define SQL_HAS_CHILDREN                    "SELECT id FROM objects WHERE parent=? LIMIT 1;"
#define SQL_GET_CHILDREN_IDS                "SELECT id FROM objects WHERE parent=? AND id > 0;"


/*
    This really nasty bit of SQL will create an object with the specified name, and
    specified class in the specified parent container, if that class is allowed in
    that container class.
*/
#define SQL_CREATE_OBJECT                                                       \
    "INSERT INTO objects (name, parent, class)"                                 \
        " SELECT ?, ?, classes.id FROM classes, containment"                    \
        " WHERE UCASE(classes.name)=UCASE(?)"                                   \
        " AND classes.id=containment.leaf"                                      \
        " AND containment.container=?"                                          \
        " LIMIT 1;"

#define SQL_RENAME_OBJECT                   "UPDATE objects SET name=? WHERE id=?;"
#define SQL_MOVE_OBJECT                     "UPDATE objects SET parent=? WHERE id=?;"
#define SQL_VERIFY_CONTAINMENT              "SELECT containment.leaf FROM containment, objects WHERE objects.id=? AND objects.class=containment.container AND containment.leaf=?;"
#define SQL_LIST_CONTAINMENT                "SELECT classes.name FROM classes, containment, objects WHERE containment.leaf=classes.id AND containment.container=objects.class AND objects.id=?;"

#define SQL_CREATE_ATTRIBUTE                "INSERT INTO attributes (name, type, single, public) VALUES (?, ?, ?, ?);"
#define SQL_CREATE_CLASS                    "INSERT INTO classes (name, container) VALUES (?, ?);"
#define SQL_CREATE_SUPERCLASS               "INSERT INTO superclasses (subclass, superclass) SELECT ?, id FROM classes where UCASE(name)=UCASE(?);"
#define SQL_CREATE_CONTAINMENT              "INSERT INTO containment (leaf, container) SELECT ?, id FROM classes where UCASE(name)=UCASE(?);"
#define SQL_CREATE_MANDATORY                "INSERT INTO mandatory (class, attribute) SELECT ?, id FROM attributes where UCASE(name)=UCASE(?);"
#define SQL_CREATE_OPTIONAL                 "INSERT INTO optional (class, attribute) SELECT ?, id FROM attributes where UCASE(name)=UCASE(?);"
#define SQL_CREATE_NAMING                   "INSERT INTO naming (class, attribute) SELECT ?, id FROM attributes where UCASE(name)=UCASE(?);"

#define SQL_GET_OBJECT_CLASS                "SELECT class FROM objects WHERE id=?;"
#define SQL_GET_OBJECT_CLASSNAME            "SELECT classes.name FROM classes, objects WHERE objects.id=? AND objects.class=classes.id;"


#define SQL_GET_NAMING                      "SELECT attributes.name FROM attributes, naming WHERE attributes.id=naming.attribute and naming.class=?;"
#define SQL_GET_MANDATORY                   "SELECT attributes.name FROM attributes, mandatory WHERE attributes.id=mandatory.attribute and mandatory.class=?;"
#define SQL_GET_MANDATORY_BY_NAME           "SELECT attributes.name FROM attributes, classes, mandatory WHERE"          \
                                                " mandatory.attribute=attributes.id AND mandatory.class=classes.id"     \
                                                " and UCASE(classes.name)=UCASE(?);"
#define SQL_GET_PARENT_INFO                 "SELECT parent, name FROM objects WHERE id=?;"

/* If the specified attribute can be written to the specified object then a single row will be returned, with the attribute ID and a BOOL specifiying single or multi value */
#define SQL_GET_OBJECT_ATTR_INFO            "SELECT attributes.id, attributes.single, attributes.type FROM attributes, objects, optional WHERE objects.id=? AND objects.class=optional.class AND UCASE(attributes.name)=UCASE(?) AND attributes.id=optional.attribute LIMIT 1;"

#define SQL_ADD_DN_VALUE                    "INSERT INTO data (object, attribute, dnvalue) VALUES (?, ?, ?);"
#define SQL_REMOVE_DN_VALUE                 "DELETE FROM data WHERE object=? AND attribute=? AND dnvalue=?;"
#define SQL_ADD_BOOL_VALUE                  "INSERT INTO data (object, attribute, boolvalue) VALUES (?, ?, ?);"
#define SQL_REMOVE_BOOL_VALUE               "DELETE FROM data WHERE object=? AND attribute=? AND boolvalue=?;"
#define SQL_ADD_STRING_VALUE                "INSERT INTO data (object, attribute, value, complete) VALUES (?, ?, ?, ?);"
#define SQL_REMOVE_STRING_VALUE             "DELETE FROM data WHERE object=? AND attribute=? AND value=?;"

#define SQL_READ_VALUE                      "SELECT data.value, data.dnvalue, data.boolvalue, data.complete, attributes.type FROM data, attributes WHERE data.object=? AND data.attribute=attributes.id AND UCASE(attributes.name)=UCASE(?) ORDER BY data.id;"

/* Copy data from all superclasses to a new class */
#define SQL_COPY_SUPERCLASSES               "INSERT INTO superclasses (subclass, superclass) SELECT ?, superclasses.superclass FROM superclasses, classes WHERE superclasses.subclass=classes.id AND UCASE(classes.name)=UCASE(?);"
#define SQL_COPY_CONTAINMENT                "INSERT INTO containment (container, leaf) SELECT containment.container, superclasses.subclass FROM containment, superclasses WHERE containment.leaf=superclasses.superclass and superclasses.subclass=?;"
#define SQL_COPY_MANDATORY                  "INSERT INTO mandatory (attribute, class) SELECT mandatory.attribute, superclasses.subclass FROM mandatory, superclasses WHERE mandatory.class=superclasses.superclass and superclasses.subclass=?;"
#define SQL_COPY_OPTIONAL                   "INSERT INTO optional (attribute, class) SELECT optional.attribute, superclasses.subclass FROM optional, superclasses WHERE optional.class=superclasses.superclass and superclasses.subclass=?;"
#define SQL_COPY_NAMING                     "INSERT INTO naming (attribute, class) SELECT naming.attribute, superclasses.subclass FROM naming, superclasses WHERE naming.class=superclasses.superclass and superclasses.subclass=?;"

#define SQL_CLEAR_ATTRIBUTE                 "DELETE FROM data WHERE object=? AND attribute=?;"

/* Delete an object */
#define SQL_DELETE_OBJECT_VALUES            "DELETE FROM data WHERE object=?;"
#define SQL_DELETE_OBJECT_RIGHTS            "DELETE FROM rights WHERE provider=?;"
#define SQL_DELETE_OBJECT                   "DELETE FROM objects WHERE id=?;"

/* Delete an attribute */
#define SQL_DELETE_ATTRIBUTE                "DELETE FROM attributes WHERE id=?;"
#define SQL_MANDATORY_DELETE_ATTR           "DELETE FROM mandatory WHERE attribute=?;"
#define SQL_OPTIONAL_DELETE_ATTR            "DELETE FROM optional WHERE attribute=?;"
#define SQL_NAMING_DELETE_ATTR              "DELETE FROM naming WHERE attribute=?;"
#define SQL_VALUES_DELETE_ATTR              "DELETE FROM data WHERE attribute=?;"

/* Delete a class */
#define SQL_GET_SUBCLASSES                  "SELECT subclass FROM superclasses WHERE superclass=?;"
#define SQL_GET_OBJECTS_BY_CLASS            "SELECT id FROM objects WHERE class=?;"
#define SQL_CONTAINMENT_DEL_CLASS           "DELETE FROM containment WHERE container=? OR leaf=?;"
#define SQL_MANDATORY_DEL_CLASS             "DELETE FROM mandatory WHERE class=?;"
#define SQL_OPTIONAL_DEL_CLASS              "DELETE FROM optional WHERE class=?;"
#define SQL_NAMING_DEL_CLASS                "DELETE FROM naming WHERE class=?;"
#define SQL_DELETE_CLASS                    "DELETE FROM classes WHERE id=?;"


#define SQL_VERIFY_PASSWORD                 "SELECT id FROM objects WHERE id=? AND pass=?;"
#define SQL_SET_PASSWORD                    "UPDATE objects SET pass=? WHERE id=?;"

/*
    We need mdb.h for the types used below, but we have to make sure it is included after
    we have defined our own ValueStruct, EnumStruct and Handle types.
*/
#include <mdb.h>

typedef struct _MDBodbcBaseSchemaAttributes {
    unsigned char *name;
    unsigned long flags;
    unsigned long type;
} MDBodbcBaseSchemaAttributes;

MDBodbcBaseSchemaAttributes MDBodbcBaseSchemaAttributesList[] = {
    { "Aliased Object Name",                69,     MDB_ATTR_SYN_DIST_NAME  },
    { "Back Link",                          268,    MDB_ATTR_SYN_DIST_NAME  },
    { "CA Private Key",                     93,     MDB_ATTR_SYN_BINARY     },
    { "CA Public Key",                      205,    MDB_ATTR_SYN_BINARY     },
    { "CN",                                 102,    MDB_ATTR_SYN_STRING     },
    { "C",                                  103,    MDB_ATTR_SYN_STRING     },
    { "Description",                        102,    MDB_ATTR_SYN_STRING     },
    { "Facsimile Telephone Number",         68,     MDB_ATTR_SYN_STRING     },
    { "Group Membership",                   580,    MDB_ATTR_SYN_DIST_NAME  },
    { "L",                                  102,    MDB_ATTR_SYN_STRING     },
    { "Login Disabled",                     69,     MDB_ATTR_SYN_BOOL       },
    { "Member",                             68,     MDB_ATTR_SYN_DIST_NAME  },
    { "EMail Address",                      228,    MDB_ATTR_SYN_STRING     },
    { "Internet EMail Address",             228,    MDB_ATTR_SYN_STRING     },
    { "departmentNumber",                   228,    MDB_ATTR_SYN_STRING     },
    { "Notify",                             68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Operator",                           324,    MDB_ATTR_SYN_DIST_NAME  },
    { "OU",                                 102,    MDB_ATTR_SYN_STRING     },
    { "O",                                  102,    MDB_ATTR_SYN_STRING     },
    { "Owner",                              68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Page Description Language",          102,    MDB_ATTR_SYN_STRING     },
    { "Path",                               68,     MDB_ATTR_SYN_STRING     },
    { "Physical Delivery Office Name",      102,    MDB_ATTR_SYN_STRING     },
    { "Postal Address",                     68,     MDB_ATTR_SYN_STRING     },
    { "Postal Code",                        102,    MDB_ATTR_SYN_STRING     },
    { "Postal Office Box",                  102,    MDB_ATTR_SYN_STRING     },
    { "Private Key",                        93,     MDB_ATTR_SYN_BINARY     },
    { "Profile",                            69,     MDB_ATTR_SYN_DIST_NAME  },
    { "Public Key",                         205,    MDB_ATTR_SYN_BINARY     },
    { "Reference",                          1052,   MDB_ATTR_SYN_DIST_NAME  },
    { "Resource",                           68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Role Occupant",                      68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Higher Privileges",                  836,    MDB_ATTR_SYN_DIST_NAME  },
    { "Security Equals",                    836,    MDB_ATTR_SYN_DIST_NAME  },
    { "See Also",                           68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Serial Number",                      102,    MDB_ATTR_SYN_STRING     },
    { "Server",                             324,    MDB_ATTR_SYN_DIST_NAME  },
    { "S",                                  102,    MDB_ATTR_SYN_STRING     },
    { "Status",                             197,    MDB_ATTR_SYN_STRING     },
    { "SA",                                 102,    MDB_ATTR_SYN_STRING     },
    { "Surname",                            230,    MDB_ATTR_SYN_STRING     },
    { "Telephone Number",                   100,    MDB_ATTR_SYN_STRING     },
    { "Title",                              102,    MDB_ATTR_SYN_STRING     },
    { "Unknown",                            4,      MDB_ATTR_SYN_STRING     },
    { "User",                               324,    MDB_ATTR_SYN_DIST_NAME  },
    { "Version",                            231,    MDB_ATTR_SYN_STRING     },
    { "Device",                             68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Language",                           69,     MDB_ATTR_SYN_STRING     },
    { "UID",                                69,     MDB_ATTR_SYN_STRING     },
    { "GID",                                69,     MDB_ATTR_SYN_STRING     },
    { "Unknown Base Class",                 39,     MDB_ATTR_SYN_STRING     },
    { "Authority Revocation",               77,     MDB_ATTR_SYN_BINARY     },
    { "Certificate Revocation",             77,     MDB_ATTR_SYN_BINARY     },
    { "Cross Certificate Pair",             68,     MDB_ATTR_SYN_BINARY     },
    { "Full Name",                          102,    MDB_ATTR_SYN_STRING     },
    { "Certificate Validity Interval",      71,     MDB_ATTR_SYN_STRING     },
    { "External Synchronizer",              68,     MDB_ATTR_SYN_BINARY     },
    { "Message Routing Group",              68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Postmaster",                         68,     MDB_ATTR_SYN_DIST_NAME  },
    { "Mailbox Location",                   197,    MDB_ATTR_SYN_DIST_NAME  },
    { "Mailbox ID",                         231,    MDB_ATTR_SYN_STRING     },
    { "External Name",                      69,     MDB_ATTR_SYN_BINARY     },
    { "Security Flags",                     69,     MDB_ATTR_SYN_STRING     },
    { "Last Referenced Time",               1029,   MDB_ATTR_SYN_STRING     },
    { "Given Name",                         230,    MDB_ATTR_SYN_STRING     },
    { "Initials",                           230,    MDB_ATTR_SYN_STRING     },
    { "Generational Qualifier",             231,    MDB_ATTR_SYN_STRING     },
    { "Profile Membership",                 580,    MDB_ATTR_SYN_DIST_NAME  },
    { "Equivalent To Me",                   324,    MDB_ATTR_SYN_DIST_NAME  },
    { "Timezone",                           133,    MDB_ATTR_SYN_BINARY     },
    { "T",                                  102,    MDB_ATTR_SYN_STRING     },
    { "Used By",                            332,    MDB_ATTR_SYN_STRING     },
    { "Uses",                               332,    MDB_ATTR_SYN_STRING     },
    { "GUID",                               207,    MDB_ATTR_SYN_BINARY     },
    { "Other GUID",                         198,    MDB_ATTR_SYN_BINARY     },
    { NULL,                                 0,      0       }
};

typedef struct _MDBodbcBaseSchemaClasses {
    unsigned char *name;
    unsigned long flags;
    unsigned char *superClass;
    unsigned char *containedBy;
    unsigned char *namedBy;
    unsigned char *mandatory;
    unsigned char *optional;
} MDBodbcBaseSchemaClasses;

MDBodbcBaseSchemaClasses MDBodbcBaseSchemaClassesList[] = {
    { "[Nothing]",              3,  NULL,                       NULL,                               NULL,               NULL,                   NULL    },
    { "Top",                    3,  NULL,                       NULL,                               "T",                NULL,                   "CA Public Key,Private Key,Certificate Validity Interval,Authority Revocation,Last Referenced Time,Equivalent To Me,Back Link,Reference,Cross Certificate Pair,Certificate Revocation,Used By,GUID,Other GUID"    },
    { "Alias",                  2,  "Top",                      NULL,                               NULL,               "Aliased Object Name",  NULL    },
    { "Country",                3,  "Top",                      "Top,Tree Root,[Nothing]",          "C",                "C",                    "Description"    },
    { "Organization",           3,  NULL,                       "Top,Tree Root,Country,Locality,[Nothing]",             "O",                    "O","Description,Facsimile Telephone Number,L,EMail Address,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,See Also,S,SA,Telephone Number"    },
    { "Organizational Unit",    3,  NULL,                       "Locality,Organization,Organizational Unit",            "OU",                   "OU","Description,Facsimile Telephone Number,L,EMail Address,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,See Also,S,SA,Telephone Number"    },
    { "Locality",               3,  "Top",                      "Locality,Country,Organizational Unit,Organization",    "L,S",                  NULL,"Description,L,See Also,S,SA"    },
    { "Organizational Role",    2,  "Top",                      "Organization,Organizational Unit", "CN",               "CN",                   "Description,Facsimile Telephone Number,L,EMail Address,OU,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,Role Occupant,See Also,S,SA,Telephone Number,Mailbox Location,Mailbox ID"    },
    { "Group",                  2,  "Top",                      "Organization,Organizational Unit", "CN",               "CN",                   "Description,L,Member,OU,O,Owner,See Also,GID,Full Name,EMail Address,Mailbox Location,Mailbox ID,Profile,Profile Membership"    },
    { "Person",                 2,  NULL,                       "Organization,Organizational Unit", "CN",               "CN",                   "Description,See Also,Telephone Number,Full Name,Given Name,Initials,Generational Qualifier"    },
    { "Organizational Person",  2,  "Person",                   NULL,                               "OU",               NULL,                   "Facsimile Telephone Number,L,EMail Address,OU,Physical Delivery Office Name,Postal Address,Postal Code,Postal Office Box,S,SA,Title,Mailbox Location,Mailbox ID"    },
    { "User",                   2,  "Organizational Person",    NULL,                               NULL,               NULL,                   "Group Membership,Login Disabled,Private Key,Profile,Public Key,Security Equals,Minimum Message Server,Language,UID,Type Creator Map,Higher Privileges,Security Flags,Profile Membership,Timezone,Surname,Internet EMail Address,departmentNumber"    },
    { "Device",                 2,  "Top",                      "Organization,Organizational Unit", "CN",               "CN",                   "Description,L,OU,O,Owner,See Also,Serial Number"    },
    { "Computer",               2,  "Device",                   NULL,                               NULL,               NULL,                   "Operator,Server,Status"    },
    { "Resource",               0,  "Top",                      "Organization,Organizational Unit", "CN",               "CN",                   "Description,L,OU,O,See Also,Uses"    },
    { "Profile",                2,  "Top",                      "Organization,Organizational Unit", "CN",               "CN",                   "Description,L,OU,O,See Also,Full Name"    },
    { "Server",                 0,  "Top",                      "Organization,Organizational Unit", "CN",               "CN",                   "Description,L,OU,O,Private Key,Public Key,Resource,See Also,Status,User,Version,Full Name,Security Equals,Security Flags,Timezone"    },
    { "NCP Server",             2,  "Server",                   NULL,                               NULL,               NULL,                   "Operator"    },
    { NULL,                     0,  NULL,                       NULL,                               NULL,               NULL,                   NULL    }
};

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



#endif /* MDBODBCP_H */

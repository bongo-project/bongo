#############################################################################
#
#  Copyright (c) 2006 Novell, Inc.
#  All Rights Reserved.
#
#  This program is free software; you can redistribute it and/or
#  modify it under the terms of version 2.1 of the GNU Lesser General Public
#  License as published by the Free Software Foundation.
#
#  This program is distributed in the hope that it will be useful,
#  but WITHOUT ANY WARRANTY; without even the implied warranty of
#  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
#  GNU Lesser General Public License for more details.
#
#  You should have received a copy of the GNU Lesser General Public License
#  along with this program; if not, contact Novell, Inc.
#
#  To contact Novell about this file by physical or electronic mail,
#  you may find current contact information at www.novell.com
#
#############################################################################

import os, random, socket
from libbongo.bootstrap import msgapi, mdb
from bongo import MDB, Xpl, Privs
from bongo.admin import Schema
from xml.dom import minidom, Node
from bongo.BongoError import BongoError

import logging
log = logging.getLogger("bongo.admin.Util")

def AddBongoUser(mdb, context, username, attributes, password = None):
    """Add a User object in the directory."""
    from libbongo.libs import msgapi as MsgApi
    userobject = username
    required = {}
    if context:
        userobject = context + '\\' + username
    else:
        if '\\' not in username:
            raise BongoError('No context for user ' + username)
    for attr in [mdb.A_SURNAME]:
        if attr not in attributes.keys():
            opt = Properties.keys()[Properties.values().index(attr)]
            raise BongoError('Required option --%s not specified.' % opt)
        required[attr] = attributes.pop(attr)
    mdb.AddObject(userobject, mdb.C_USER, required)
    for key, vals in attributes.iteritems():
        MsgApi.SetUserSetting(userobject, key, vals)
    if password:
        mdb.SetPassword(userobject, password)
    return

def ModifyBongoUser(mdb, context, username, attributes, password = None):
    """Modify a User object in the directory."""
    from libbongo.libs import msgapi as MsgApi
    userobject = username
    if context:
        userobject = context + '\\' + username
    else:
        if '\\' not in username:
            raise BongoError('No context for user ' + username)
    for key, vals in attributes.iteritems():
        if MsgApi.SetUserSetting(userobject, key, vals) is False:
            raise BongoError('Error setting %s on %s' % (key, userobject))
    if password:
        mdb.SetPassword(userobject, password)
    return

def RemoveBongoUser(mdb, context, username):
    """Remove a User object from the directory."""
    from libbongo.libs import msgapi as MsgApi
    userobject = username
    if context:
        userobject = context + '\\' + username
    else:
        if '\\' not in username:
            raise BongoError('No context for user ' + username)
    details = mdb.GetObjectDetails(userobject)
    if 'Type' not in details.keys():
        raise BongoError('Could not determine type for ' + userobject)
    if not details['Type'] == mdb.C_USER:
        raise BongoError(userobject + ' is not a ' + mdb.C_USER)
    for attr in mdb.GetAttributes(userobject, mdb.A_SEE_ALSO):
        attrdet = mdb.GetObjectDetails(attr)
        if details['Type'] == MsgApi.C_USER_SETTINGS:
            mdb.DeleteObject(attr)
    mdb.DeleteObject(userobject)
    return

def DelegateBongoUser(mdb, context, username, community):
    """Delegate a User object as a community manager."""
    from libbongo.libs import msgapi as MsgApi
    userobject = username
    if context:
        userobject = context + '\\' + username
    else:
        if '\\' not in username:
            raise BongoError('No context for user ' + username)
    details = mdb.GetObjectDetails(userobject)
    if 'Type' not in details.keys():
        raise BongoError('Could not determine type for ' + userobject)
    if not details['Type'] == mdb.C_USER:
        raise BongoError(userobject + ' is not a ' + mdb.C_USER)
    if not community:
        raise BongoError('No community specified')
    if '\\' not in community:
        #FIXME: find Bongo Services\Parent Objects equiv
        raise BongoError('Community needs full path for now')
    if not mdb.IsObject(community):
        #FIXME: schema needs changing, BongoTOMManager etc
        mdb.AddObject(community, msgapi.C_PARENTOBJECT, \
                      {msgapi.A_TOM_MANAGER:[userobject]})
    #FIXME: they come back relative to base and don't compare correctly
    attrs = mdb.GetAttributes(community, msgapi.A_TOM_MANAGER)
    if userobject in attrs:
        raise BongoError(userobject + ' is already a community ' + \
                             'manager for ' + community)
    mdb.AddAttribute(community, msgapi.A_TOM_MANAGER,  userobject)
    MsgApi.AddUserSetting(userobject, msgapi.A_PARENT_OBJECT, community)

def UndelegateBongoUser(mdb, context, username, community):
    """Undelegate a User object as a community manager."""
    userobject = username
    if context:
        userobject = context + '\\' + username
    else:
        if '\\' not in username:
            raise BongoError('No context for user ' + username)
    details = mdb.GetObjectDetails(userobject)
    if 'Type' not in details.keys():
        raise BongoError('Could not determine type for ' + userobject)
    if not details['Type'] == mdb.C_USER:
        raise BongoError(userobject + ' is not a ' + mdb.C_USER)
    if not community:
        raise BongoError('No community specified')
    if '\\' not in community:
        #FIXME: find Bongo Services\Parent Objects equiv
        raise BongoError('Community needs full path for now')
    if not mdb.IsObject(community):
        #FIXME: schema needs changing, BongoTOMManager etc
        raise BongoError('Community ' + community + 'not found')
    #FIXME: they come back relative to base and don't compare correctly
    attrs = mdb.GetAttributes(community, msgapi.A_TOM_MANAGER)
    if userobject not in attrs:
        raise BongoError(userobject + ' is not a community manager ' + \
                             'for ' + community)
    attrs.remove(userobject)
    mdb.SetAttribute(community, msgapi.A_TOM_MANAGER, attrs)
    MsgApi.SetUserSetting(userobject, msgapi.A_PARENT_OBJECT, [])

def ListBongoUsers(mdb, context):
    """Return a list of all User objects in a given context."""
    return mdb.EnumerateObjects(context, mdb.C_USER)

def AddBongoAgent(mdb, server, name, type, attributes = {}):
    """Add an agent object under a server in the directory."""
    agentobj = '%s\\%s\\%s' % \
               (msgapi.GetConfigProperty(msgapi.BONGO_SERVICES), server, name)
    if not server or not mdb.IsObject(agentobj.split('\\', 1)[0]):
        raise BongoError('Server \"%s\" not found' % str(server))
    for attr in [msgapi.A_MODULE_NAME,
                 msgapi.A_MODULE_VERSION]:
        if attr not in attributes.keys():
            opt = Properties.keys()[Properties.values().index(attr)]
            raise BongoError('Required option --%s not specified' % opt)
    if type == msgapi.C_STORE:
        for attr in [msgapi.A_MESSAGE_STORE]:
            opt = Properties.keys()[Properties.values().index(attr)]
            if attr not in attributes.keys():
                raise BongoError('Required option --%s not specified'% opt)
    elif type == msgapi.C_SMTP:
        for attr in [msgapi.A_DOMAIN]:
            opt = Properties.keys()[Properties.values().index(attr)]
            if attr not in attributes.keys():
                raise BongoError('Required option --%s not specified'% opt)
    mdb.AddObject(agentobj, Entities[type], attributes)
    return

def ModifyBongoAgent(mdb, server, name, attributes = {}):
    """Modify an agent object in the directory."""
    agentobj = '%s\\%s\\%s' % \
               (msgapi.GetConfigProperty(msgapi.BONGO_SERVICES), server, name)
    if not server or not mdb.IsObject(agentobj):
        raise BongoError('Server \"%s\" not found' % str(server))
    if attributes:
        for key, vals in attributes.iteritems():
            mdb.SetAttribute(agentobj, key, vals)
    return

def RemoveBongoAgent(mdb, server, name):
    """Remove an agent from a server in the directory."""
    agentobj = '%s\\%s\\%s' % \
               (msgapi.GetConfigProperty(msgapi.BONGO_SERVICES), server, name)
    mdb.DeleteObject(agentobj)
    return

def AddBongoServer(mdb, name, attributes = {}):
    """Add a BongoMessagingServer object in the directory."""
    serverobj = '%s\\%s' % \
                (msgapi.GetConfigProperty(msgapi.BONGO_SERVICES), name)
    for attr in [mdb.A_ORGANIZATIONAL_UNIT_NAME,
                 msgapi.A_POSTMASTER,
                 msgapi.A_CONTEXT,
                 msgapi.A_OFFICIAL_NAME]:
        if attr not in attributes.keys():
            opt = Properties.keys()[Properties.values().index(attr)]
            raise BongoError('Required option --%s not specified' % opt)
    mdb.AddObject(serverobj, msgapi.C_SERVER, attributes)
    # Always create a user settings container for bongo services
    settingsobj = '%s\\%s' % (serverobj, msgapi.USER_SETTINGS_ROOT)
    mdb.AddObject(settingsobj, msgapi.C_USER_SETTINGS_CONTAINER, {})
    return

def ModifyBongoServer(mdb, name, attributes = {}):
    """Modify a BongoMessagingServer object in the directory."""
    serverobj = '%s\\%s' % \
               (msgapi.GetConfigProperty(msgapi.BONGO_SERVICES), name)
    if not mdb.IsObject(serverobj):
        raise BongoError('Server \"%s\" not found' % str(serverobj))
    for key, vals in attributes.iteritems():
        mdb.SetAttribute(serverobj, key, vals)
    return

def RemoveBongoServer(mdb, name):
    """Remove a BongoMessagingServer from a server in the directory."""
    serverobj = '%s\\%s' % \
               (msgapi.GetConfigProperty(msgapi.BONGO_SERVICES), name)
    settingsobj = '%s\\%s' % (serverobj, msgapi.USER_SETTINGS_ROOT)
    if not mdb.IsObject(settingsobj):
        raise BongoError('User setting root \"%s\" not found' %
                             str(settingsobj))
    mdb.DeleteObject(settingsobj)
    if not mdb.IsObject(serverobj):
        raise BongoError('Server \"%s\" not found' % str(serverobj))
    mdb.DeleteObject(serverobj)
    return

def ListBongoMessagingServers(mdb):
    """Return a list of all messaging servers in the directory."""
    context = msgapi.GetConfigProperty(msgapi.BONGO_SERVICES)
    return mdb.EnumerateObjects(context, msgapi.C_SERVER)

def ListBongoAgents(mdb, messagingserver):
    """Return a list of all agents under a server in the directory."""
    context = msgapi.GetConfigProperty(msgapi.BONGO_SERVICES)
    agents = mdb.EnumerateObjects(context + '\\' + messagingserver, None)
    tempagents = agents[:]
    for agent in tempagents:
        #FIXME: add an MDB function that enumerates with superclasses
        #       and above use C_AGENT
        agentdn = '%s\\%s\\%s' % (context, messagingserver, agent)
        if not mdb.GetAttributes(agentdn, msgapi.A_MODULE_NAME):
            #print 'no module name in '+agent
            agents.remove(agent)
    return agents

schema = None
def GetSchema():
    global schema
    if schema is None:
        schema = Schema.Schema(SCHEMA_XML)
        schema.read(BASE_XML)

    return schema

def GetClassAttributes(classname):
    """Return a list of possible attributes for a given classname."""
    attrs = []

    schema = GetSchema()

    tempattrs = FindInheritedAttrs(unicode(classname), schema, [])
    for attr in tempattrs:
        attrs.append(str(attr))

    return attrs

def GetClassAttributeObjects(classname):
    """Return a list of possible attributes for a given classname."""
    schema = GetSchema()
    attrs = FindInheritedAttrObjects(unicode(classname), schema)

    # add the naming attribute for this class (typically CN), which
    # isn't found in the schema
    cls = schema.classes[classname]

    cn = Schema.SchemaAttribute(None)
    cn.name = "CN"
    cn.syntax = "CaseInsensitiveString"
    cn.syncImmediate = True
    cn.singleValue = False

    attrs["CN"] = cn

    return attrs

def FindInheritedAttrObjects(classname, schema):
    """Recursively find all attributes for a class (and its superclasses)"""
    attrs = {}

    cls = schema.classes[classname]

    for attr in cls.allowedAttributes:
        attrs[attr] = schema.getAttribute(attr)

    for attr in cls.requiredAttributes:
        attrs[attr] = schema.getAttribute(attr)

    for superclass in cls.superClasses:
        if schema.classes.has_key(superclass):
            attrs.update(FindInheritedAttrObjects(superclass, schema))
            
    return attrs

def FindInheritedAttrs(classname, schema, attrs):
    """Recursively find all attributes for a class (and its superclasses)"""
    attrs.extend(schema.classes[classname].allowedAttributes)
    attrs.extend(schema.classes[classname].requiredAttributes)
    for superclass in schema.classes[classname].superClasses:
        if superclass in schema.classes.keys():
            FindInheritedAttrs(superclass, schema, attrs)
            
    return attrs

def GetUserAttributes(mdb, context, username):
    """Return a dict of (set and unset) attributes on a User."""
    from libbongo.libs import msgapi as MsgApi
    fields = {}
    userobject = username
    if context:
        userobject = context + '\\' + username
    else:
        if '\\' not in username:
            raise BongoError('No context for user ' + username)
    details = mdb.GetObjectDetails(userobject)
    if 'Type' not in details.keys():
        raise BongoError('Could not determine type for ' + userobject)
    if not details['Type'] == mdb.C_USER:
        raise BongoError(userobject + ' is not a ' + mdb.C_USER)
    for attribute in GetClassAttributes(mdb.C_USER):
        attrs = MsgApi.GetUserSetting(userobject, attribute)
        fields[str(attribute)] = attrs
    for attribute in GetClassAttributes(msgapi.C_USER_SETTINGS):
        attrs = MsgApi.GetUserSetting(userobject, attribute)
        fields[str(attribute)] = attrs
    return fields

def GetObjectDetails(mdb, dn):
    context = msgapi.GetConfigProperty(msgapi.BONGO_SERVICES)
    dn = context + '\\' + dn
    return mdb.GetObjectDetails(dn)

#agent = 'Bongo Messaging Server\\Some Agent'
def GetAgentAttributes(mdb, agent):
    """Return a dict of (set and unset) attributes on an Agent."""
    fields = {}
    context = msgapi.GetConfigProperty(msgapi.BONGO_SERVICES)
    agent = '%s\\%s' % (context, agent)
    details = mdb.GetObjectDetails(agent)

    agentType = details.get('Type')

    if not agentType:
        raise BongoError('Could not determine type for %s' % agent)
    for attribute in GetClassAttributes(agentType):
        attrs = mdb.GetAttributes(agent, attribute)
        fields[str(attribute)] = attrs

    return fields

def GetServerAttributes(mdb, server):
    """Return a dict of (set and unset) attributes on a Server."""
    fields = {}
    context = msgapi.GetConfigProperty(msgapi.BONGO_SERVICES)
    server = '%s\\%s' % (context, server)
    for attribute in GetClassAttributes(msgapi.C_SERVER):
        attrs = mdb.GetAttributes(server, attribute)
        fields[str(attribute)] = attrs
    return fields

def SetupFromXML(mdb, base, xmlfile):
    """Build Bongo's directory based on an XML setup file"""
    objs = {}  # {'objDN': objType, ...} objects we'll add
    attrs = {} # {'objDN': {attr:[val, v2], ...}, ...) attributes for each obj
    passwds = {} # {'objDN': passwd, ...} passwords for (user) objects
    doc = minidom.parse(xmlfile)
    schema = Schema.Schema(SCHEMA_XML)
    schema.read(BASE_XML)
    for node in doc.documentElement.childNodes:
        if node.nodeType == Node.ELEMENT_NODE:
            SetupFromNode(mdb, node, objs, attrs, passwds, schema, base)
    #we need to add objects and attrs in two passes so DN refs are valid
    requiredattrs = {}
    allowedattrs = {}
    #speaking of DNs, we need to resolve them now
    for key, attrib in attrs.iteritems():
        requiredattrs[key] = {}
        allowedattrs[key] = {}
        for attr, vals in attrib.iteritems():
            newvals = []
            for val in vals:
                newval = None
                if '\\' in val and not val.strip('\\'):
                    #if it's only a slash (or several)
                    newval = '%s\\' % base
                elif val.startswith('\\'):
                    #fill in DNs
                    for obj in objs.keys():
                        if obj.endswith(val):
                            newval = obj
                    if not newval:
                        #could be a user. TODO: ponder this some more
                        newval = '%s\\%s' % (base, val.strip('\\'))
                else:
                    newval = val
                newvals.append(newval)
            type = unicode(objs[key])
            #required = schema.classes[type].requiredAttributes
            required = SetupFindInheritedRequiredAttrs(type, schema)
            if unicode(Properties[attr]) in required:
                requiredattrs[key][Properties[attr]] = newvals
            else:
                allowedattrs[key][Properties[attr]] = newvals
    objects = objs.items()
    objects.sort()
    #very handy sort, \a now preceeds \a\b, which preceeds \a\b\c...
    #create objects with just the required attributes first
    for objname, classname in objects:
        attributes = {}
        if objname in requiredattrs.keys():
            attributes = requiredattrs[objname]
        if(mdb.IsObject(objname)):
            #print 'modifying existing %s' % objname
            #only set attributes if object already exists
            #TODO: overridable behaviour for ignoring/clearing existing objs?
            for key, vals in attributes.iteritems():
                mdb.SetAttribute(objname, key, vals)
        else:
            #print 'adding %s (%s)' % (objname, classname)
            #print attributes
            log.debug("adding %s (%s): %s", objname, classname, str(attributes))
            mdb.AddObject(objname, classname, attributes)
        if objname in passwds.keys():
            mdb.SetPassword(objname, passwds[objname])
    #now slap on all the optional attributes (in which DNs will now be valid)
    for obj, attrib in allowedattrs.iteritems():
        if objs[obj] == mdb.C_USER:
            try:
                from libbongo.libs import msgapi as MsgApi
            except:
                #TODO: for setup, chop users out of the xml file,
                #      save it in /tmp somewhere, do the rest of the
                #      bootstrapping, and import the users later
                raise BongoError('Cannot import users during setup; import from a separate file afterwards.')
            for key, vals in attrib.iteritems():
                MsgApi.SetUserSetting(obj, key, vals)
        else:
            for key, vals in attrib.iteritems():
                mdb.SetAttribute(obj, key, vals)

def SetupFindInheritedRequiredAttrs(classname, schema, attrs = []):
    """Recursively find required attributes for a class"""
    attrs.extend(schema.classes[classname].requiredAttributes)
    for superclass in schema.classes[classname].superClasses:
        if superclass in schema.classes.keys():
            SetupFindInheritedRequiredAttrs(superclass, schema, attrs)
    return attrs

def SetupFromNode(mdb, node, objs, attrs, passwds, schema, base):
    """Recursive helper to SetupFromXML"""
    recurse = True
    parent = ''
    parnode = node
    if parnode.parentNode:
        if str(parnode.parentNode.nodeName) == 'user':
            ##context = msgapi.GetConfigProperty(msgapi.DEFAULT_CONTEXT)
            #we might not be able to ask for that yet, we'll have to build it
            if 'context' in node.attributes.keys():
                context = str(node.attributes['context'])
            else:
                context = mdb.baseDn
            parent = '%s\\%s' % \
                (context, str(parnode.parentNode.attributes['name'].value))
        else:
            while parnode.parentNode:
                if parnode.parentNode.attributes:
                    if 'name' in parnode.parentNode.attributes.keys():
                        parname = \
                            str(parnode.parentNode.attributes['name'].value)
                        parent = '%s\\%s' % (parname, parent)
                parnode = parnode.parentNode
            parent = '%s\\%s' % (base, parent.strip('\\'))
    parent = '\\%s' % parent.strip('\\')
    #parent now has \\com\\novell\\Bongo Services\\... (or the user DN)
    if str(node.nodeName) in ['module', 'agent', 'container'] and \
        'type' in node.attributes.keys():
        objname = str(node.attributes['name'].value)
        if objname in objs.keys():
            raise BongoError('Duplicate setup item \"%s\" found' % objname)
        itemname = Entities[str(node.attributes['type'].value)]
        objs['%s\\%s' % (parent, objname)] = itemname
    #import users
    elif str(node.nodeName) == 'user' and 'name' in node.attributes.keys():
        passwd = None
        if 'context' in node.attributes.keys():
            context = str(node.attributes['context'])
        else:
            ##context = msgapi.GetConfigProperty(msgapi.DEFAULT_CONTEXT)
            #we might not be able to ask for that yet, we'll have to build it
            context = mdb.baseDn
        if 'password' in node.attributes.keys():
            password = str(node.attributes['password'].value)
            passwds['%s\\%s' % (context, node.attributes['name'].value)] = \
                             password
        objs['%s\\%s' % (context, str(node.attributes['name'].value))] = \
                      mdb.C_USER
    elif str(node.nodeName) in Entities.keys():
        objname = str(node.attributes[u'name'].value)
        if objname in objs.keys():
            raise BongoError('Duplicate setup item \"%s\" found' % objname)
        itemname = Entities[str(node.nodeName)]
        ##objs['%s\\%s%s' % (base, parent, objname)] = itemname
        objs['%s\\%s' % (parent, objname)] = itemname
        #add properties to bongo.conf if they're absent
        confprop = {}
        if str(node.nodeName) == 'services':
            confprop[msgapi.BONGO_SERVICES] = '%s\\%s' % \
                                             (parent, objname)
            objs['%s\\%s\\%s' % (parent, objname, msgapi.USER_SETTINGS_ROOT)] \
                              = msgapi.C_USER_SETTINGS_CONTAINER
        elif str(node.nodeName) == 'messaging-server':
            confprop[msgapi.MESSAGING_SERVER] = '%s\\%s' % \
                                                (parent, objname)
        if confprop:
            ModifyConfProperties(confprop)
    elif str(node.nodeName) == 'property':
        recurse = False #we'll crawl the rest of the way right here
        ##parent = '%s\\%s' % (base, parent.strip('\\'))
        key = str(node.attributes['name'].value)
        values = []
        if parent not in attrs.keys():
            attrs[parent] = {}
        if key not in Properties.keys():
            raise BongoError('Unrecognized property: %s' % key)
        #grab <property><value>value</value></property> style vals
        # + <property>value</property> style vals
        for elem in node.getElementsByTagName('value') + [node]:
            for child in elem.childNodes:
                if child.nodeValue:
                    if child.nodeValue.strip():
                        val = str(child.nodeValue.strip())
                        if unicode(Properties[key]) in schema.attributes.keys():
                            syn = schema.attributes[unicode(Properties[key])].syntax
                            if syn == 'DistinguishedName':
                                values.append('\\%s' % val)
                            else:
                                values.append(val)
                        else:
                            raise BongoError('Unrecognized attr: %s' % \
                                                 Properties[key])
        attrs[parent][key] = values
    #elif str(node.nodeName) in ['list-container']:
        ##TODO: deal with lists
        #print 'Notice: %s not yet supported, continuing...' % node.nodeName
    else:
        raise BongoError('Unrecognized: %s' % node.nodeName)
    if recurse:
        for child in node.childNodes:
            if child.nodeType == Node.ELEMENT_NODE:
                SetupFromNode(mdb, child, objs, attrs, passwds, schema, base)

def GeneratePassword(size, seed=None):
    """Generate a random password"""
    # Not a very robust password, but it works for now.
    posschars = 'abcdefghijklmnopqrstuvwxyz' + \
                'ABCDEFGHIJKLMNOPQRSTUVWXYZ' + \
                '1234567890'
    random.seed(seed)
    password = ''
    for i in range(size):
        password += random.choice(posschars)
    return password

def ReadConfProperties():
    """Read the bongo.conf file"""
    if not os.path.exists(Xpl.DEFAULT_CONF_DIR):
        os.makedirs(Xpl.DEFAULT_CONF_DIR)

    filename = os.path.join(Xpl.DEFAULT_CONF_DIR, msgapi.CONFIG_FILENAME)
    if not os.path.exists(filename):
        return {}

    fd = open(filename)

    props = {}
    for line in fd.readlines():
        line = line.strip()
        if line == "":
            continue

        key, val = line.split("=", 1)
        props[key] = val

    fd.close()
    return props

def WriteConfProperties(props):
    """Write the bongo.conf file"""
    if not os.path.exists(Xpl.DEFAULT_CONF_DIR):
        os.makedirs(Xpl.DEFAULT_CONF_DIR)

    filename = os.path.join(Xpl.DEFAULT_CONF_DIR, msgapi.CONFIG_FILENAME)
    fd = open(filename, "w")

    keys = props.keys()
    keys.sort()

    for key in keys:
        fd.write("%s=%s\n" % (key, props[key]))

    fd.close()
    Privs.Chown(filename)

def ModifyConfProperties(props):
    """Add or modify configuration properties in bongo.conf"""
    fileProps = ReadConfProperties()
    fileProps.update(props)
    WriteConfProperties(fileProps)

SCHEMA_XML = '%s/bongo-schema.xml' % Xpl.DEFAULT_CONF_DIR
BASE_XML = '%s/base-schema.xml' % Xpl.DEFAULT_CONF_DIR

Agents = (
    msgapi.C_ADDRESSBOOK,
    msgapi.C_ALIAS,
    msgapi.C_ANTISPAM,
    msgapi.C_ANTIVIRUS,
    msgapi.C_CALCMD,
    msgapi.C_COLLECTOR,
    msgapi.C_CONNMGR,
    msgapi.C_IMAP,
    msgapi.C_ITIP,
    msgapi.C_POP,
    msgapi.C_PROXY,
    msgapi.C_QUEUE,
    msgapi.C_RULESRV,
    msgapi.C_SIGNUP,
    msgapi.C_SMTP,
    msgapi.C_STORE
    )


Entities = { \
    'services': msgapi.C_ROOT, \
    'messaging-server': msgapi.C_SERVER, \
    'webadmin': msgapi.C_WEBADMIN, \
    'addressbook-agent': msgapi.C_ADDRESSBOOK, \
    'agent': msgapi.C_AGENT, \
    'alias-agent': msgapi.C_ALIAS, \
    'antispam-agent': msgapi.C_ANTISPAM, \
    'finger-agent': msgapi.C_FINGER, \
    'gatekeeper-agent': msgapi.C_GATEKEEPER, \
    'conn-mgr-agent': msgapi.C_CONNMGR, \
    'imap-agent': msgapi.C_IMAP, \
    'list': msgapi.C_LIST, \
    'ndslist': msgapi.C_NDSLIST, \
    'listcontainer': msgapi.C_LISTCONTAINER, \
    'listuser': msgapi.C_LISTUSER, \
    'list-agent': msgapi.C_LISTAGENT, \
    'store-agent': msgapi.C_STORE, \
    'queue-agent': msgapi.C_QUEUE, \
    'pop-agent': msgapi.C_POP, \
    'proxy-agent': msgapi.C_PROXY, \
    'rule-agent': msgapi.C_RULESRV, \
    'signup-agent': msgapi.C_SIGNUP, \
    'smtp-agent': msgapi.C_SMTP, \
    'community-container': msgapi.C_PARENTCONTAINER, \
    'community': msgapi.C_PARENTOBJECT, \
    'templatecontainer': msgapi.C_TEMPLATECONTAINER, \
    'itip-agent': msgapi.C_ITIP, \
    'antivirus-agent': msgapi.C_ANTIVIRUS, \
    'resource': msgapi.C_RESOURCE, \
    'connmgr-module': msgapi.C_CONNMGR_MODULE, \
    'cm-user-module': msgapi.C_CM_USER_MODULE, \
    'cm-lists-module': msgapi.C_CM_LISTS_MODULE, \
    'cm-rbl-module': msgapi.C_CM_RBL_MODULE, \
    'cm-rdns-module': msgapi.C_CM_RDNS_MODULE, \
    'calcmd-agent': msgapi.C_CALCMD, \
    'collector-agent': msgapi.C_COLLECTOR, \
    'user': msgapi.C_USER, \
    'organizational-role': msgapi.C_ORGANIZATIONAL_ROLE, \
    'organization': msgapi.C_ORGANIZATION, \
    'organizational-unit': msgapi.C_ORGANIZATIONAL_UNIT, \
    'group': msgapi.C_GROUP, \
    'user-settings-container': msgapi.C_USER_SETTINGS_CONTAINER, \
    'user-settings': msgapi.C_USER_SETTINGS, \
    'afp_server': mdb.C_AFP_SERVER, \
    'alias': mdb.C_ALIAS, \
    'bindery_object': mdb.C_BINDERY_OBJECT, \
    'bindery_queue': mdb.C_BINDERY_QUEUE, \
    'commexec': mdb.C_COMMEXEC, \
    'computer': mdb.C_COMPUTER, \
    'country': mdb.C_COUNTRY, \
    'device': mdb.C_DEVICE, \
    'directory_map': mdb.C_DIRECTORY_MAP, \
    'external_entity': mdb.C_EXTERNAL_ENTITY, \
    'group': mdb.C_GROUP, \
    'list': mdb.C_LIST, \
    'locality': mdb.C_LOCALITY, \
    'message_routing_group': mdb.C_MESSAGE_ROUTING_GROUP, \
    'messaging_routing_group': mdb.C_MESSAGING_ROUTING_GROUP, \
    'messaging_server': mdb.C_MESSAGING_SERVER, \
    'ncp_server': mdb.C_NCP_SERVER, \
    'organization': mdb.C_ORGANIZATION, \
    'organizational_person': mdb.C_ORGANIZATIONAL_PERSON, \
    'organizational_role': mdb.C_ORGANIZATIONAL_ROLE, \
    'organizational_unit': mdb.C_ORGANIZATIONAL_UNIT, \
    'partition': mdb.C_PARTITION, \
    'person': mdb.C_PERSON, \
    'print_server': mdb.C_PRINT_SERVER, \
    'printer': mdb.C_PRINTER, \
    'profile': mdb.C_PROFILE, \
    'queue': mdb.C_QUEUE, \
    'resource': mdb.C_RESOURCE, \
    'server': mdb.C_SERVER, \
    'top': mdb.C_TOP, \
    'tree_root': mdb.C_TREE_ROOT, \
    'unknown': mdb.C_UNKNOWN, \
    'user': mdb.C_USER, \
    'volume': mdb.C_VOLUME, \
    }

Properties = { \
    'agent-status': msgapi.A_AGENT_STATUS, \
    'alias': msgapi.A_ALIAS, \
    'authentication-required': msgapi.A_AUTHENTICATION_REQUIRED, \
    'configuration': msgapi.A_CONFIGURATION, \
    'client': msgapi.A_CLIENT, \
    'context': msgapi.A_CONTEXT, \
    'domain': msgapi.A_DOMAIN, \
    'bongo-email-address': msgapi.A_EMAIL_ADDRESS, \
    'disabled': msgapi.A_DISABLED, \
    'finger-message': msgapi.A_FINGER_MESSAGE, \
    'host': msgapi.A_HOST, \
    'ip-address': msgapi.A_IP_ADDRESS, \
    'ldap-server': msgapi.A_LDAP_SERVER, \
    'ldap-nmap-server': msgapi.A_LDAP_NMAP_SERVER, \
    'ldap-search-dn': msgapi.A_LDAP_SEARCH_DN, \
    'message-limit': msgapi.A_MESSAGE_LIMIT, \
    'messaging-server': msgapi.A_MESSAGING_SERVER, \
    'module-name': msgapi.A_MODULE_NAME, \
    'module-version': msgapi.A_MODULE_VERSION, \
    'monitored-queue': msgapi.A_MONITORED_QUEUE, \
    'queue-server': msgapi.A_QUEUE_SERVER, \
    'store-server': msgapi.A_STORE_SERVER, \
    'official-name': msgapi.A_OFFICIAL_NAME, \
    'quota-message': msgapi.A_QUOTA_MESSAGE, \
    'bongo-postmaster': msgapi.A_POSTMASTER, \
    'reply-message': msgapi.A_REPLY_MESSAGE, \
    'url': msgapi.A_URL, \
    'resolver': msgapi.A_RESOLVER, \
    'routing': msgapi.A_ROUTING, \
    'server-status': msgapi.A_SERVER_STATUS, \
    'queue-interval': msgapi.A_QUEUE_INTERVAL, \
    'queue-timeout': msgapi.A_QUEUE_TIMEOUT, \
    'bongo-uid': msgapi.A_UID, \
    'vacation-message': msgapi.A_VACATION_MESSAGE, \
    'word': msgapi.A_WORD, \
    'message-store': msgapi.A_MESSAGE_STORE, \
    'forwarding-address': msgapi.A_FORWARDING_ADDRESS, \
    'forwarding-enabled': msgapi.A_FORWARDING_ENABLED, \
    'autoreply-enabled': msgapi.A_AUTOREPLY_ENABLED, \
    'autoreply-message': msgapi.A_AUTOREPLY_MESSAGE, \
    'port': msgapi.A_PORT, \
    'ssl-port': msgapi.A_SSL_PORT, \
    'snmp-description': msgapi.A_SNMP_DESCRIPTION, \
    'snmp-version': msgapi.A_SNMP_VERSION, \
    'snmp-organization': msgapi.A_SNMP_ORGANIZATION, \
    'snmp-location': msgapi.A_SNMP_LOCATION, \
    'snmp-contact': msgapi.A_SNMP_CONTACT, \
    'snmp-name': msgapi.A_SNMP_NAME, \
    'messaging-disabled': msgapi.A_MESSAGING_DISABLED, \
    'store-trusted-hosts': msgapi.A_STORE_TRUSTED_HOSTS, \
    'quota-value': msgapi.A_QUOTA_VALUE, \
    'scms-user-threshold': msgapi.A_SCMS_USER_THRESHOLD, \
    'scms-size-threshold': msgapi.A_SCMS_SIZE_THRESHOLD, \
    'smtp-verify-address': msgapi.A_SMTP_VERIFY_ADDRESS, \
    'smtp-allow-auth': msgapi.A_SMTP_ALLOW_AUTH, \
    'smtp-allow-vrfy': msgapi.A_SMTP_ALLOW_VRFY, \
    'smtp-allow-expn': msgapi.A_SMTP_ALLOW_EXPN, \
    'work-directory': msgapi.A_WORK_DIRECTORY, \
    'loglevel': msgapi.A_LOGLEVEL, \
    'minimum-space': msgapi.A_MINIMUM_SPACE, \
    'smtp-send-etrn': msgapi.A_SMTP_SEND_ETRN, \
    'smtp-accept-etrn': msgapi.A_SMTP_ACCEPT_ETRN, \
    'limit-remote-processing': msgapi.A_LIMIT_REMOTE_PROCESSING, \
    'limit-remote-start-wd': msgapi.A_LIMIT_REMOTE_START_WD, \
    'limit-remote-end-wd': msgapi.A_LIMIT_REMOTE_END_WD, \
    'limit-remote-start-we': msgapi.A_LIMIT_REMOTE_START_WE, \
    'limit-remote-end-we': msgapi.A_LIMIT_REMOTE_END_WE, \
    'product-version': msgapi.A_PRODUCT_VERSION, \
    'proxy-list': msgapi.A_PROXY_LIST, \
    'maximum-items': msgapi.A_MAXIMUM_ITEMS, \
    'time-interval': msgapi.A_TIME_INTERVAL, \
    'relayhost': msgapi.A_RELAYHOST, \
    'alias-options': msgapi.A_ALIAS_OPTIONS, \
    'ldap-options': msgapi.A_LDAP_OPTIONS, \
    'custom-alias': msgapi.A_CUSTOM_ALIAS, \
    'advertising-config': msgapi.A_ADVERTISING_CONFIG, \
    'bongo-language': msgapi.A_LANGUAGE, \
    'preferences': msgapi.A_PREFERENCES, \
    'quota-warning': msgapi.A_QUOTA_WARNING, \
    'queue-tuning': msgapi.A_QUEUE_TUNING, \
    'timeout': msgapi.A_TIMEOUT, \
    'privacy': msgapi.A_PRIVACY, \
    'thread-limit': msgapi.A_THREAD_LIMIT, \
    'dbf-pagesize': msgapi.A_DBF_PAGESIZE, \
    'dbf-keysize': msgapi.A_DBF_KEYSIZE, \
    'addressbook-config': msgapi.A_ADDRESSBOOK_CONFIG, \
    'addressbook-url-system': msgapi.A_ADDRESSBOOK_URL_SYSTEM, \
    'addressbook-url-public': msgapi.A_ADDRESSBOOK_URL_PUBLIC, \
    'addressbook': msgapi.A_ADDRESSBOOK, \
    'server-standalone': msgapi.A_SERVER_STANDALONE, \
    'forward-undeliverable': msgapi.A_FORWARD_UNDELIVERABLE, \
    'phrase': msgapi.A_PHRASE, \
    'accounting-enabled': msgapi.A_ACCOUNTING_ENABLED, \
    'accounting-data': msgapi.A_ACCOUNTING_DATA, \
    'billing-data': msgapi.A_BILLING_DATA, \
    'blocked-address': msgapi.A_BLOCKED_ADDRESS, \
    'allowed-address': msgapi.A_ALLOWED_ADDRESS, \
    'recipient-limit': msgapi.A_RECIPIENT_LIMIT, \
    'rbl-host': msgapi.A_RBL_HOST, \
    'signature': msgapi.A_SIGNATURE, \
    'conn-mgr': msgapi.A_CONNMGR, \
    'conn-mgr-config': msgapi.A_CONNMGR_CONFIG, \
    'user-domain': msgapi.A_USER_DOMAIN, \
    'rts-antispam-config': msgapi.A_RTS_ANTISPAM_CONFIG, \
    'spool-directory': msgapi.A_SPOOL_DIRECTORY, \
    'scms-directory': msgapi.A_SCMS_DIRECTORY, \
    'dbf-directory': msgapi.A_DBF_DIRECTORY, \
    'nls-directory': msgapi.A_NLS_DIRECTORY, \
    'bin-directory': msgapi.A_BIN_DIRECTORY, \
    'lib-directory': msgapi.A_LIB_DIRECTORY, \
    'default-charset': msgapi.A_DEFAULT_CHARSET, \
    'rule': msgapi.A_RULE, \
    'relay-domain': msgapi.A_RELAY_DOMAIN, \
    'list-digesttime': msgapi.A_LIST_DIGESTTIME, \
    'list-abstract': msgapi.A_LIST_ABSTRACT, \
    'list-description': msgapi.A_LIST_DESCRIPTION, \
    'list-configuration': msgapi.A_LIST_CONFIGURATION, \
    'list-store': msgapi.A_LIST_STORE, \
    'list-nmapstore': msgapi.A_LIST_NMAPSTORE, \
    'list-moderator': msgapi.A_LIST_MODERATOR, \
    'list-owner': msgapi.A_LIST_OWNER, \
    'list-signature': msgapi.A_LIST_SIGNATURE, \
    'list-signature-html': msgapi.A_LIST_SIGNATURE_HTML, \
    'list-digest-version': msgapi.A_LIST_DIGEST_VERSION, \
    'list-members': msgapi.A_LIST_MEMBERS, \
    'listuser-options': msgapi.A_LISTUSER_OPTIONS, \
    'listuser-adminoptions': msgapi.A_LISTUSER_ADMINOPTIONS, \
    'listuser-password': msgapi.A_LISTUSER_PASSWORD, \
    'feature-set': msgapi.A_FEATURE_SET, \
    'private-key-location': msgapi.A_PRIVATE_KEY_LOCATION, \
    'certificate-location': msgapi.A_CERTIFICATE_LOCATION, \
    'config-changed': msgapi.A_CONFIG_CHANGED, \
    'listserver-name': msgapi.A_LISTSERVER_NAME, \
    'list-welcome-message': msgapi.A_LIST_WELCOME_MESSAGE, \
    'community': msgapi.A_PARENT_OBJECT, \
    'feature-inheritance': msgapi.A_FEATURE_INHERITANCE, \
    'template': msgapi.A_TEMPLATE, \
    'timezone': msgapi.A_TIMEZONE, \
    'locale': msgapi.A_LOCALE, \
    'password-configuration': msgapi.A_PASSWORD_CONFIGURATION, \
    'bongo-title': msgapi.A_TITLE, \
    'default-template': msgapi.A_DEFAULT_TEMPLATE, \
    'tom-manager': msgapi.A_TOM_MANAGER, \
    'tom-contexts': msgapi.A_TOM_CONTEXTS, \
    'tom-domains': msgapi.A_TOM_DOMAINS, \
    'tom-options': msgapi.A_TOM_OPTIONS, \
    'description': msgapi.A_DESCRIPTION, \
    'statserver-1': msgapi.A_STATSERVER_1, \
    'statserver-2': msgapi.A_STATSERVER_2, \
    'statserver-3': msgapi.A_STATSERVER_3, \
    'statserver-4': msgapi.A_STATSERVER_4, \
    'statserver-5': msgapi.A_STATSERVER_5, \
    'statserver-6': msgapi.A_STATSERVER_6, \
    'news': msgapi.A_NEWS, \
    'manager': msgapi.A_MANAGER, \
    'available-shares': msgapi.A_AVAILABLE_SHARES, \
    'owned-shares': msgapi.A_OWNED_SHARES, \
    'new-share-message': msgapi.A_NEW_SHARE_MESSAGE, \
    'available-proxies': msgapi.A_AVAILABLE_PROXIES, \
    'owned-proxies': msgapi.A_OWNED_PROXIES, \
    'resource-manager': msgapi.A_RESOURCE_MANAGER, \
    'bongo-messaging-server': msgapi.A_BONGO_MESSAGING_SERVER, \
    'bongo-acl': msgapi.A_ACL, \
    'wa-default-template': msgapi.A_WA_DEFAULT_TEMPLATE, \
    'wa-allowed-templates': msgapi.A_WA_ALLOWED_TEMPLATES, \
    'wa-disallowed-templates': msgapi.A_WA_DISALLOWED_TEMPLATES, \
    'sms-authorized-phones': msgapi.A_SMS_AUTHORIZED_PHONES, \
    'calcmd-address-suffix': msgapi.A_CALCMD_ADDRESS_SUFFIX, \
    'account_balance': mdb.A_ACCOUNT_BALANCE, \
    'acl': mdb.A_ACL, \
    'aliased_object_name': mdb.A_ALIASED_OBJECT_NAME, \
    'allow_unlimited_credit': mdb.A_ALLOW_UNLIMITED_CREDIT, \
    'authority_revocation': mdb.A_AUTHORITY_REVOCATION, \
    'back_link': mdb.A_BACK_LINK, \
    'bindery_object_restriction': mdb.A_BINDERY_OBJECT_RESTRICTION, \
    'bindery_property': mdb.A_BINDERY_PROPERTY, \
    'bindery_type': mdb.A_BINDERY_TYPE, \
    'cartridge': mdb.A_CARTRIDGE, \
    'ca_private_key': mdb.A_CA_PRIVATE_KEY, \
    'ca_public_key': mdb.A_CA_PUBLIC_KEY, \
    'certificate_revocation': mdb.A_CERTIFICATE_REVOCATION, \
    'certificate_validity_interval': mdb.A_CERTIFICATE_VALIDITY_INTERVAL, \
    'convergence': mdb.A_CONVERGENCE, \
    'country-name': mdb.A_COUNTRY_NAME, \
    'cross-certificate-pair': mdb.A_CROSS_CERTIFICATE_PAIR, \
    'default-queue': mdb.A_DEFAULT_QUEUE, \
    'description': mdb.A_DESCRIPTION, \
    'detect-intruder': mdb.A_DETECT_INTRUDER, \
    'device': mdb.A_DEVICE, \
    'ds-revision': mdb.A_DS_REVISION, \
    'email-address': mdb.A_EMAIL_ADDRESS, \
    'equivalent-to-me': mdb.A_EQUIVALENT_TO_ME, \
    'external-name': mdb.A_EXTERNAL_NAME, \
    'external-synchronizer': mdb.A_EXTERNAL_SYNCHRONIZER, \
    'fax-number': mdb.A_FACSIMILE_TELEPHONE_NUMBER, \
    'full-name': mdb.A_FULL_NAME, \
    'generational-qualifier': mdb.A_GENERATIONAL_QUALIFIER, \
    'gid': mdb.A_GID, \
    'given-name': mdb.A_GIVEN_NAME, \
    'group-membership': mdb.A_GROUP_MEMBERSHIP, \
    'high-sync-interval': mdb.A_HIGH_SYNC_INTERVAL, \
    'higher-privileges': mdb.A_HIGHER_PRIVILEGES, \
    'home-directory': mdb.A_HOME_DIRECTORY, \
    'host-device': mdb.A_HOST_DEVICE, \
    'host-resource-name': mdb.A_HOST_RESOURCE_NAME, \
    'host-server': mdb.A_HOST_SERVER, \
    'inherited-acl': mdb.A_INHERITED_ACL, \
    'initials': mdb.A_INITIALS, \
    'internet-email-address': mdb.A_INTERNET_EMAIL_ADDRESS, \
    'intruder-attempt-reset-intrvl': mdb.A_INTRUDER_ATTEMPT_RESET_INTRVL, \
    'intruder-lockout-reset-intrvl': mdb.A_INTRUDER_LOCKOUT_RESET_INTRVL, \
    'locality-name': mdb.A_LOCALITY_NAME, \
    'language': mdb.A_LANGUAGE, \
    'last-login-time': mdb.A_LAST_LOGIN_TIME, \
    'last-referenced-time': mdb.A_LAST_REFERENCED_TIME, \
    'locked-by-intruder': mdb.A_LOCKED_BY_INTRUDER, \
    'lockout-after-detection': mdb.A_LOCKOUT_AFTER_DETECTION, \
    'login-allowed-time_map': mdb.A_LOGIN_ALLOWED_TIME_MAP, \
    'login-disabled': mdb.A_LOGIN_DISABLED, \
    'login-expiration-time': mdb.A_LOGIN_EXPIRATION_TIME, \
    'login-grace-limit': mdb.A_LOGIN_GRACE_LIMIT, \
    'login-grace-remaining': mdb.A_LOGIN_GRACE_REMAINING, \
    'login-intruder-address': mdb.A_LOGIN_INTRUDER_ADDRESS, \
    'login-intruder-attempts': mdb.A_LOGIN_INTRUDER_ATTEMPTS, \
    'login-intruder-limit': mdb.A_LOGIN_INTRUDER_LIMIT, \
    'login-intruder-reset_time': mdb.A_LOGIN_INTRUDER_RESET_TIME, \
    'login-maximum-simultaneous': mdb.A_LOGIN_MAXIMUM_SIMULTANEOUS, \
    'login-script': mdb.A_LOGIN_SCRIPT, \
    'login-time': mdb.A_LOGIN_TIME, \
    'low-reset-time': mdb.A_LOW_RESET_TIME, \
    'low-sync-interval': mdb.A_LOW_SYNC_INTERVAL, \
    'mailbox-id': mdb.A_MAILBOX_ID, \
    'mailbox-location': mdb.A_MAILBOX_LOCATION, \
    'member': mdb.A_MEMBER, \
    'memory': mdb.A_MEMORY, \
    'message-server': mdb.A_MESSAGE_SERVER, \
    'message-routing-group': mdb.A_MESSAGE_ROUTING_GROUP, \
    'messaging-database-location': mdb.A_MESSAGING_DATABASE_LOCATION, \
    'messaging-routing-group': mdb.A_MESSAGING_ROUTING_GROUP, \
    'messaging-server': mdb.A_MESSAGING_SERVER, \
    'messaging-server-type': mdb.A_MESSAGING_SERVER_TYPE, \
    'minimum-account_balance': mdb.A_MINIMUM_ACCOUNT_BALANCE, \
    'network-address': mdb.A_NETWORK_ADDRESS, \
    'network-address-restriction': mdb.A_NETWORK_ADDRESS_RESTRICTION, \
    'nns-domain': mdb.A_NNS_DOMAIN, \
    'notify': mdb.A_NOTIFY, \
    'obituary': mdb.A_OBITUARY, \
    'organization-name': mdb.A_ORGANIZATION_NAME, \
    'object-class': mdb.A_OBJECT_CLASS, \
    'operator': mdb.A_OPERATOR, \
    'organizational-unit-name': mdb.A_ORGANIZATIONAL_UNIT_NAME, \
    'owner': mdb.A_OWNER, \
    'page-description-language': mdb.A_PAGE_DESCRIPTION_LANGUAGE, \
    'partition-control': mdb.A_PARTITION_CONTROL, \
    'partition-creation_time': mdb.A_PARTITION_CREATION_TIME, \
    'partition-status': mdb.A_PARTITION_STATUS, \
    'password-allow-change': mdb.A_PASSWORD_ALLOW_CHANGE, \
    'password-expiration-interval': mdb.A_PASSWORD_EXPIRATION_INTERVAL, \
    'password-expiration-time': mdb.A_PASSWORD_EXPIRATION_TIME, \
    'password-management': mdb.A_PASSWORD_MANAGEMENT, \
    'password-minimum-length': mdb.A_PASSWORD_MINIMUM_LENGTH, \
    'password-required': mdb.A_PASSWORD_REQUIRED, \
    'password-unique-required': mdb.A_PASSWORD_UNIQUE_REQUIRED, \
    'passwords-used': mdb.A_PASSWORDS_USED, \
    'path': mdb.A_PATH, \
    'permanent-config-parms': mdb.A_PERMANENT_CONFIG_PARMS, \
    'physical-delivery': mdb.A_PHYSICAL_DELIVERY_OFFICE_NAME, \
    'postal-address': mdb.A_POSTAL_ADDRESS, \
    'postal-code': mdb.A_POSTAL_CODE, \
    'postal-office-box': mdb.A_POSTAL_OFFICE_BOX, \
    'postmaster': mdb.A_POSTMASTER, \
    'print-server': mdb.A_PRINT_SERVER, \
    'private-key': mdb.A_PRIVATE_KEY, \
    'printer': mdb.A_PRINTER, \
    'printer-configuration': mdb.A_PRINTER_CONFIGURATION, \
    'printer-control': mdb.A_PRINTER_CONTROL, \
    'print-job-configuration': mdb.A_PRINT_JOB_CONFIGURATION, \
    'profile': mdb.A_PROFILE, \
    'profile-membership': mdb.A_PROFILE_MEMBERSHIP, \
    'public-key': mdb.A_PUBLIC_KEY, \
    'queue': mdb.A_QUEUE, \
    'queue-directory': mdb.A_QUEUE_DIRECTORY, \
    'received-up-to': mdb.A_RECEIVED_UP_TO, \
    'reference': mdb.A_REFERENCE, \
    'replica': mdb.A_REPLICA, \
    'replica-up-to': mdb.A_REPLICA_UP_TO, \
    'resource': mdb.A_RESOURCE, \
    'revision': mdb.A_REVISION, \
    'role-occupant': mdb.A_ROLE_OCCUPANT, \
    'state-or-province-name': mdb.A_STATE_OR_PROVINCE_NAME, \
    'street-address': mdb.A_STREET_ADDRESS, \
    'sap-name': mdb.A_SAP_NAME, \
    'security-equals': mdb.A_SECURITY_EQUALS, \
    'security-flags': mdb.A_SECURITY_FLAGS, \
    'see-also': mdb.A_SEE_ALSO, \
    'serial-number': mdb.A_SERIAL_NUMBER, \
    'bongo-server': mdb.A_SERVER, \
    'server-holds': mdb.A_SERVER_HOLDS, \
    'status': mdb.A_STATUS, \
    'supported-connections': mdb.A_SUPPORTED_CONNECTIONS, \
    'supported-gateway': mdb.A_SUPPORTED_GATEWAY, \
    'supported-services': mdb.A_SUPPORTED_SERVICES, \
    'supported-typefaces': mdb.A_SUPPORTED_TYPEFACES, \
    'surname': mdb.A_SURNAME, \
    'in-sync-up-to': mdb.A_IN_SYNC_UP_TO, \
    'telephone-number': mdb.A_TELEPHONE_NUMBER, \
    'title': mdb.A_TITLE, \
    'type-creator-map': mdb.A_TYPE_CREATOR_MAP, \
    'uid': mdb.A_UID, \
    'unknown': mdb.A_UNKNOWN, \
    'unknown-base-class': mdb.A_UNKNOWN_BASE_CLASS, \
    'version': mdb.A_VERSION, \
    'volume': mdb.A_VOLUME, \
    'max-load': msgapi.A_MAX_LOAD, \
    'acl-enabled': msgapi.A_ACL_ENABLED, \
    'ube-smtp-after-pop': msgapi.A_UBE_SMTP_AFTER_POP, \
    'ube-remote-auth-only': msgapi.A_UBE_REMOTE_AUTH_ONLY, \
    'max-flood-count': msgapi.A_MAX_FLOOD_COUNT, \
    'max-null-server-recips': msgapi.A_MAX_NULL_SENDER_RECIPS, \
    'max-mx-servers': msgapi.A_MAX_MX_SERVERS, \
    'relay-local-mail': msgapi.A_RELAY_LOCAL_MAIL, \
    'clustered': msgapi.A_CLUSTERED, \
    'force-bind': msgapi.A_FORCE_BIND, \
    'ssl-tls': msgapi.A_SSL_TLS, \
    'ssl-allow-chained': msgapi.A_SSL_ALLOW_CHAINED, \
    'bounce-return': msgapi.A_BOUNCE_RETURN, \
    'bounce-cc-postmaster': msgapi.A_BOUNCE_CC_POSTMASTER, \
    'quota-user': msgapi.A_QUOTA_USER, \
    'quota-system': msgapi.A_QUOTA_SYSTEM, \
    'register-queue': msgapi.A_REGISTER_QUEUE, \
    'queue-limit-bounces': msgapi.A_QUEUE_LIMIT_BOUNCES, \
    'limit-bounces-entries': msgapi.A_LIMIT_BOUNCE_ENTRIES, \
    'limit-bounces-interval': msgapi.A_LIMIT_BOUNCE_INTERVAL, \
    'domain-routing': msgapi.A_DOMAIN_ROUTING, \
    'monitor-interval': msgapi.A_MONITOR_INTERVAL, \
    'manager-port': msgapi.A_MANAGER_PORT, \
    'manager-ssl-port': msgapi.A_MANAGER_SSL_PORT, \
    # this was left out previously for some unknown reason..
    'common-name': mdb.A_COMMON_NAME, \
    }
#purposely left out 'user': mdb.A_USER, \

# reverse the Properties hash
PropertyArguments = {}
for key, value in Properties.items():
    PropertyArguments[value] = key

#TODO: translations
EntitiesPrintable = { \
    msgapi.C_ROOT: 'Bongo Services', \
    msgapi.C_SERVER: 'Bongo Messaging Server', \
    msgapi.C_WEBADMIN: 'Web Admin', \
    msgapi.C_ADDRESSBOOK: 'Address Book Agent', \
    msgapi.C_AGENT: 'Agent', \
    msgapi.C_ALIAS: 'Alias Agent', \
    msgapi.C_ANTISPAM: 'Anti-Spam Agent', \
    msgapi.C_FINGER: 'Finger Agent', \
    msgapi.C_GATEKEEPER: 'Gatekeeper', \
    msgapi.C_CONNMGR: 'Connection Manager Agent', \
    msgapi.C_IMAP: 'IMAP Agent', \
    msgapi.C_LIST: 'List', \
    msgapi.C_NDSLIST: 'NDS List', \
    msgapi.C_LISTCONTAINER: 'List Container', \
    msgapi.C_LISTUSER: 'List User', \
    msgapi.C_LISTAGENT: 'List Agent', \
    msgapi.C_STORE: 'Store Agent', \
    msgapi.C_QUEUE: 'Queue Agent', \
    msgapi.C_POP: 'POP Agent', \
    msgapi.C_PROXY: 'Proxy Agent', \
    msgapi.C_RULESRV: 'Rule Agent', \
    msgapi.C_SIGNUP: 'Signup Agent', \
    msgapi.C_SMTP: 'SMTP Agent', \
    msgapi.C_PARENTCONTAINER: 'Community Container', \
    msgapi.C_PARENTOBJECT: 'Community', \
    msgapi.C_TEMPLATECONTAINER: 'Template Container', \
    msgapi.C_ITIP: 'ITIP Agent', \
    msgapi.C_ANTIVIRUS: 'Anti-Virus Agent', \
    msgapi.C_RESOURCE: 'Resource', \
    msgapi.C_CONNMGR_MODULE: 'Connection Manager Module', \
    msgapi.C_CM_USER_MODULE: 'Connection Manager User Module', \
    msgapi.C_CM_LISTS_MODULE: 'Connection Manager Lists Module', \
    msgapi.C_CM_RBL_MODULE: 'Connection Manager RBL Module', \
    msgapi.C_CM_RDNS_MODULE: 'Connection Manager RDNS Module', \
    msgapi.C_CALCMD: 'CalCmd Agent', \
    msgapi.C_COLLECTOR: 'Collector Agent', \
    msgapi.C_USER: 'User', \
    msgapi.C_ORGANIZATIONAL_ROLE: 'Organizational Role', \
    msgapi.C_ORGANIZATION: 'Organization', \
    msgapi.C_ORGANIZATIONAL_UNIT: 'Organizational Unit', \
    msgapi.C_GROUP: 'Group', \
    msgapi.C_USER_SETTINGS_CONTAINER: 'User Settings Container', \
    msgapi.C_USER_SETTINGS: 'User Settings', \
    mdb.C_AFP_SERVER: 'AFP Server', \
    mdb.C_ALIAS: 'Alias', \
    mdb.C_BINDERY_OBJECT: 'Bindery Object', \
    mdb.C_BINDERY_QUEUE: 'Bindery Queue', \
    mdb.C_COMMEXEC: 'Comm Exec', \
    mdb.C_COMPUTER: 'Computer', \
    mdb.C_COUNTRY: 'Country', \
    mdb.C_DEVICE: 'Device', \
    mdb.C_DIRECTORY_MAP: 'Directory Map', \
    mdb.C_EXTERNAL_ENTITY: 'External Entity', \
    mdb.C_GROUP: 'Group', \
    mdb.C_LIST: 'List', \
    mdb.C_LOCALITY: 'Locality', \
    mdb.C_MESSAGE_ROUTING_GROUP: 'Message Routing Rroup', \
    mdb.C_MESSAGING_ROUTING_GROUP: 'Messaging Routing Group', \
    mdb.C_MESSAGING_SERVER: 'Messaging Server', \
    mdb.C_NCP_SERVER: 'NCP Server', \
    mdb.C_ORGANIZATION: 'Organization', \
    mdb.C_ORGANIZATIONAL_PERSON: 'Organizational Person', \
    mdb.C_ORGANIZATIONAL_ROLE: 'Organizational Role', \
    mdb.C_ORGANIZATIONAL_UNIT: 'Organizational Unit', \
    mdb.C_PARTITION: 'Partition', \
    mdb.C_PERSON: 'Person', \
    mdb.C_PRINT_SERVER: 'Print Server', \
    mdb.C_PRINTER: 'Printer', \
    mdb.C_PROFILE: 'Profile', \
    mdb.C_QUEUE: 'Queue', \
    mdb.C_RESOURCE: 'Resource', \
    mdb.C_SERVER: 'Server', \
    mdb.C_TOP: 'Top', \
    mdb.C_TREE_ROOT: 'Tree Root', \
    mdb.C_UNKNOWN: 'Unknown', \
    mdb.C_USER: 'User', \
    mdb.C_VOLUME: 'Volume', \
    }

#TODO: translations
PropertiesPrintable = { \
    mdb.A_ACCOUNT_BALANCE: 'Account Balance', \
    mdb.A_ACL: 'Access Control List', \
    mdb.A_ALIASED_OBJECT_NAME: 'Aliased Object Name', \
    mdb.A_ALLOW_UNLIMITED_CREDIT: 'Allow Unlimited Credit', \
    mdb.A_AUTHORITY_REVOCATION: 'Authority Revocation', \
    mdb.A_BACK_LINK: 'Back Link', \
    mdb.A_BINDERY_OBJECT_RESTRICTION: 'Bindery Object Restriction', \
    mdb.A_BINDERY_PROPERTY: 'Bindery Property', \
    mdb.A_BINDERY_TYPE: 'Bindery Type', \
    mdb.A_CARTRIDGE: 'Cartridge', \
    mdb.A_CA_PRIVATE_KEY: 'CA Private Key', \
    mdb.A_CA_PUBLIC_KEY: 'CA Public Key', \
    mdb.A_CERTIFICATE_REVOCATION: 'Certificate Revocation', \
    mdb.A_CERTIFICATE_VALIDITY_INTERVAL: 'Certificate Validity Interval', \
    mdb.A_COMMON_NAME: 'Common Name', \
    mdb.A_CONVERGENCE: 'Convergence', \
    mdb.A_COUNTRY_NAME: 'Country Name', \
    mdb.A_CROSS_CERTIFICATE_PAIR: 'Cross Certificate Pair', \
    mdb.A_DEFAULT_QUEUE: 'Default Queue', \
    mdb.A_DESCRIPTION: 'Description', \
    mdb.A_DETECT_INTRUDER: 'Detect Intruder', \
    mdb.A_DEVICE: 'Device', \
    mdb.A_DS_REVISION: 'DS Revision', \
    mdb.A_EMAIL_ADDRESS: 'Email Address', \
    mdb.A_EQUIVALENT_TO_ME: 'Equivalent to Me', \
    mdb.A_EXTERNAL_NAME: 'External Name', \
    mdb.A_EXTERNAL_SYNCHRONIZER: 'External Synchronizer', \
    mdb.A_FACSIMILE_TELEPHONE_NUMBER: 'Facsimile Telephone Number', \
    mdb.A_FULL_NAME: 'Full Name', \
    mdb.A_GENERATIONAL_QUALIFIER: 'Generational Qualifier', \
    mdb.A_GID: 'GID', \
    mdb.A_GIVEN_NAME: 'Given Name', \
    mdb.A_GROUP_MEMBERSHIP: 'Group Membership', \
    mdb.A_HIGH_SYNC_INTERVAL: 'High Sync Interval', \
    mdb.A_HIGHER_PRIVILEGES: 'Higher Privileges', \
    mdb.A_HOME_DIRECTORY: 'Home Directory', \
    mdb.A_HOST_DEVICE: 'Host Device', \
    mdb.A_HOST_RESOURCE_NAME: 'Host Resource Name', \
    mdb.A_HOST_SERVER: 'Host Server', \
    mdb.A_INHERITED_ACL: 'Inherited Access Control List', \
    mdb.A_INITIALS: 'Initials', \
    mdb.A_INTERNET_EMAIL_ADDRESS: 'Internet Email Address', \
    mdb.A_INTRUDER_ATTEMPT_RESET_INTRVL: 'Intruder Attempt Reset Interval', \
    mdb.A_INTRUDER_LOCKOUT_RESET_INTRVL: 'Intruder Lockout Reset Interval', \
    mdb.A_LOCALITY_NAME: 'Locality Name', \
    mdb.A_LANGUAGE: 'Language', \
    mdb.A_LAST_LOGIN_TIME: 'Last Login Time', \
    mdb.A_LAST_REFERENCED_TIME: 'Last Referenced Time', \
    mdb.A_LOCKED_BY_INTRUDER: 'Locked By Intruder', \
    mdb.A_LOCKOUT_AFTER_DETECTION: 'Lockout After Detection', \
    mdb.A_LOGIN_ALLOWED_TIME_MAP: 'Login Allowed Time Map', \
    mdb.A_LOGIN_DISABLED: 'Login Disabled', \
    mdb.A_LOGIN_EXPIRATION_TIME: 'Login Expiration Time', \
    mdb.A_LOGIN_GRACE_LIMIT: 'Login Grace Limit', \
    mdb.A_LOGIN_GRACE_REMAINING: 'Login Grace Remaining', \
    mdb.A_LOGIN_INTRUDER_ADDRESS: 'Login Intruder Address', \
    mdb.A_LOGIN_INTRUDER_ATTEMPTS: 'Login Intruder Attempts', \
    mdb.A_LOGIN_INTRUDER_LIMIT: 'Login Intruder Limit', \
    mdb.A_LOGIN_INTRUDER_RESET_TIME: 'Login Intruder Reset Time', \
    mdb.A_LOGIN_MAXIMUM_SIMULTANEOUS: 'Login Maximum Simultaneous', \
    mdb.A_LOGIN_SCRIPT: 'Login Script', \
    mdb.A_LOGIN_TIME: 'Login Time', \
    mdb.A_LOW_RESET_TIME: 'Low Reset Time', \
    mdb.A_LOW_SYNC_INTERVAL: 'Low Sync Interval', \
    mdb.A_MAILBOX_ID: 'Mailbox ID', \
    mdb.A_MAILBOX_LOCATION: 'Mailbox Location', \
    mdb.A_MEMBER: 'Member', \
    mdb.A_MEMORY: 'Memory', \
    mdb.A_MESSAGE_SERVER: 'Message Server', \
    mdb.A_MESSAGE_ROUTING_GROUP: 'Message Routing Group', \
    mdb.A_MESSAGING_DATABASE_LOCATION: 'Messaging Database Location', \
    mdb.A_MESSAGING_ROUTING_GROUP: 'Messaging Routing Group', \
    mdb.A_MESSAGING_SERVER: 'Messaging Server', \
    mdb.A_MESSAGING_SERVER_TYPE: 'Messaging Server Type', \
    mdb.A_MINIMUM_ACCOUNT_BALANCE: 'Minimum Account Balance', \
    mdb.A_NETWORK_ADDRESS: 'Network Address', \
    mdb.A_NETWORK_ADDRESS_RESTRICTION: 'Network Address Restriction', \
    mdb.A_NNS_DOMAIN: 'NNS Domain', \
    mdb.A_NOTIFY: 'Notify', \
    mdb.A_OBITUARY: 'Obituary', \
    mdb.A_ORGANIZATION_NAME: 'Organization Name', \
    mdb.A_OBJECT_CLASS: 'Object Class', \
    mdb.A_OPERATOR: 'Operator', \
    mdb.A_ORGANIZATIONAL_UNIT_NAME: 'Organizational Unit Name', \
    mdb.A_OWNER: 'Owner', \
    mdb.A_PAGE_DESCRIPTION_LANGUAGE: 'Page Description Language', \
    mdb.A_PARTITION_CONTROL: 'Partition Control', \
    mdb.A_PARTITION_CREATION_TIME: 'Partition Creation Time', \
    mdb.A_PARTITION_STATUS: 'Partition Status', \
    mdb.A_PASSWORD_ALLOW_CHANGE: 'Password Allow Change', \
    mdb.A_PASSWORD_EXPIRATION_INTERVAL: 'Password Expiration Interval', \
    mdb.A_PASSWORD_EXPIRATION_TIME: 'Password Expiration Time', \
    mdb.A_PASSWORD_MANAGEMENT: 'Password Management', \
    mdb.A_PASSWORD_MINIMUM_LENGTH: 'Password Minimum Length', \
    mdb.A_PASSWORD_REQUIRED: 'Password Required', \
    mdb.A_PASSWORD_UNIQUE_REQUIRED: 'Password Unique Required', \
    mdb.A_PASSWORDS_USED: 'Passwords Used', \
    mdb.A_PATH: 'Path', \
    mdb.A_PERMANENT_CONFIG_PARMS: 'Permanent Config Parmeters', \
    mdb.A_PHYSICAL_DELIVERY_OFFICE_NAME: 'Physical Delivery Office Name', \
    mdb.A_POSTAL_ADDRESS: 'Postal Address', \
    mdb.A_POSTAL_CODE: 'Postal Code', \
    mdb.A_POSTAL_OFFICE_BOX: 'Postal Office Box', \
    mdb.A_POSTMASTER: 'Postmaster', \
    mdb.A_PRINT_SERVER: 'Print Server', \
    mdb.A_PRIVATE_KEY: 'Private Key', \
    mdb.A_PRINTER: 'Printer', \
    mdb.A_PRINTER_CONFIGURATION: 'Printer Configuration', \
    mdb.A_PRINTER_CONTROL: 'Printer Control', \
    mdb.A_PRINT_JOB_CONFIGURATION: 'Print Job Configuration', \
    mdb.A_PROFILE: 'Profile', \
    mdb.A_PROFILE_MEMBERSHIP: 'Profile Membership', \
    mdb.A_PUBLIC_KEY: 'Public Key', \
    mdb.A_QUEUE: 'Queue', \
    mdb.A_QUEUE_DIRECTORY: 'Queue Directory', \
    mdb.A_RECEIVED_UP_TO: 'Received Up To', \
    mdb.A_REFERENCE: 'Reference', \
    mdb.A_REPLICA: 'Replica', \
    mdb.A_REPLICA_UP_TO: 'Replica Up To', \
    mdb.A_RESOURCE: 'Resource', \
    mdb.A_REVISION: 'Revision', \
    mdb.A_ROLE_OCCUPANT: 'Role Occupant', \
    mdb.A_STATE_OR_PROVINCE_NAME: 'State Or Province Name', \
    mdb.A_STREET_ADDRESS: 'Street Address', \
    mdb.A_SAP_NAME: 'SAP Name', \
    mdb.A_SECURITY_EQUALS: 'Security Equals', \
    mdb.A_SECURITY_FLAGS: 'Security Flags', \
    mdb.A_SEE_ALSO: 'See Also', \
    mdb.A_SERIAL_NUMBER: 'Serial Number', \
    mdb.A_SERVER: 'Server', \
    mdb.A_SERVER_HOLDS: 'Server Holds', \
    mdb.A_STATUS: 'Status', \
    mdb.A_SUPPORTED_CONNECTIONS: 'Supported Connections', \
    mdb.A_SUPPORTED_GATEWAY: 'Supported Gateway', \
    mdb.A_SUPPORTED_SERVICES: 'Supported Services', \
    mdb.A_SUPPORTED_TYPEFACES: 'Supported Typefaces', \
    mdb.A_SURNAME: 'Surname', \
    mdb.A_IN_SYNC_UP_TO: 'In Sync Up To', \
    mdb.A_TELEPHONE_NUMBER: 'Telephone Number', \
    mdb.A_TITLE: 'Title', \
    mdb.A_TYPE_CREATOR_MAP: 'Type Creator Map', \
    mdb.A_UID: 'UID', \
    mdb.A_UNKNOWN: 'Unknown', \
    mdb.A_UNKNOWN_BASE_CLASS: 'Unknown Base Class', \
    mdb.A_USER: 'User', \
    mdb.A_VERSION: 'Version', \
    mdb.A_VOLUME: 'Volume', \
    msgapi.A_AGENT_STATUS: 'Agent Status', \
    msgapi.A_ALIAS: 'Alias', \
    msgapi.A_AUTHENTICATION_REQUIRED: 'Authentication Required', \
    msgapi.A_CONFIGURATION: 'Configuration', \
    msgapi.A_CLIENT: 'Client', \
    msgapi.A_CONTEXT: 'Context', \
    msgapi.A_DOMAIN: 'Domain', \
    msgapi.A_EMAIL_ADDRESS: 'Bongo Email Address', \
    msgapi.A_DISABLED: 'Disabled', \
    msgapi.A_FINGER_MESSAGE: 'Finger Message', \
    msgapi.A_HOST: 'Host', \
    msgapi.A_IP_ADDRESS: 'IP Address', \
    msgapi.A_LDAP_SERVER: 'LDAP Server', \
    msgapi.A_LDAP_NMAP_SERVER: 'LDAP NMAP Server', \
    msgapi.A_LDAP_SEARCH_DN: 'LDAP Search DN', \
    msgapi.A_MESSAGE_LIMIT: 'Message Limit', \
    msgapi.A_MESSAGING_SERVER: 'Messaging Server', \
    msgapi.A_MODULE_NAME: 'Module Name', \
    msgapi.A_MODULE_VERSION: 'Module Version', \
    msgapi.A_MONITORED_QUEUE: 'Monitored Queue', \
    msgapi.A_QUEUE_SERVER: 'Queue Server', \
    msgapi.A_STORE_SERVER: 'Store Server', \
    msgapi.A_OFFICIAL_NAME: 'Official Name', \
    msgapi.A_QUOTA_MESSAGE: 'Quota Message', \
    msgapi.A_QUOTA_SYSTEM: 'Quota System',
    msgapi.A_POSTMASTER: 'Postmaster', \
    msgapi.A_REPLY_MESSAGE: 'Reply Message', \
    msgapi.A_URL: 'URL', \
    msgapi.A_RESOLVER: 'Resolver', \
    msgapi.A_ROUTING: 'Routing', \
    msgapi.A_SERVER_STATUS: 'Server Status', \
    msgapi.A_QUEUE_INTERVAL: 'Queue Interval', \
    msgapi.A_QUEUE_TIMEOUT: 'Queue Timeout', \
    msgapi.A_UID: 'UID', \
    msgapi.A_VACATION_MESSAGE: 'Vacation Message', \
    msgapi.A_WORD: 'Word', \
    msgapi.A_MESSAGE_STORE: 'Message Store', \
    msgapi.A_FORWARDING_ADDRESS: 'Forwarding Address', \
    msgapi.A_FORWARDING_ENABLED: 'Forwarding Enabled', \
    msgapi.A_AUTOREPLY_ENABLED: 'Autoreply Enabled', \
    msgapi.A_AUTOREPLY_MESSAGE: 'Autoreply Message', \
    msgapi.A_PORT: 'Port', \
    msgapi.A_SSL_PORT: 'SSL Port', \
    msgapi.A_SNMP_DESCRIPTION: 'SNMP Description', \
    msgapi.A_SNMP_VERSION: 'SNMP Version', \
    msgapi.A_SNMP_ORGANIZATION: 'SNMP Organization', \
    msgapi.A_SNMP_LOCATION: 'SNMP Location', \
    msgapi.A_SNMP_CONTACT: 'SNMP Contact', \
    msgapi.A_SNMP_NAME: 'SNMP Name', \
    msgapi.A_MESSAGING_DISABLED: 'Messaging Disabled', \
    msgapi.A_STORE_TRUSTED_HOSTS: 'Store Trusted Hosts', \
    msgapi.A_QUOTA_VALUE: 'Quota Value', \
    msgapi.A_SCMS_USER_THRESHOLD: 'SCMS User Threshold', \
    msgapi.A_SCMS_SIZE_THRESHOLD: 'SCMS Size Threshold', \
    msgapi.A_SMTP_VERIFY_ADDRESS: 'SMTP Verify Address', \
    msgapi.A_SMTP_ALLOW_AUTH: 'SMTP Allow Auth', \
    msgapi.A_SMTP_ALLOW_VRFY: 'SMTP Allow Verify', \
    msgapi.A_SMTP_ALLOW_EXPN: 'SMTP Allow Expand', \
    msgapi.A_WORK_DIRECTORY: 'Work Directory', \
    msgapi.A_LOGLEVEL: 'Log Level', \
    msgapi.A_MINIMUM_SPACE: 'Minimum Space', \
    msgapi.A_SMTP_SEND_ETRN: 'SMTP Send Extended Turn', \
    msgapi.A_SMTP_ACCEPT_ETRN: 'SMTP Accept Extended Turn', \
    msgapi.A_LIMIT_REMOTE_PROCESSING: 'Limit Remote Processing', \
    msgapi.A_LIMIT_REMOTE_START_WD: 'Limit Remote Start WD', \
    msgapi.A_LIMIT_REMOTE_END_WD: 'Limit Remote End WD', \
    msgapi.A_LIMIT_REMOTE_START_WE: 'Limit Remote Start WE', \
    msgapi.A_LIMIT_REMOTE_END_WE: 'Limit Remote End WE', \
    msgapi.A_PRODUCT_VERSION: 'Product Version', \
    msgapi.A_PROXY_LIST: 'Proxy List', \
    msgapi.A_MAXIMUM_ITEMS: 'Maximum Items', \
    msgapi.A_TIME_INTERVAL: 'Time Interval', \
    msgapi.A_RELAYHOST: 'Relay Host', \
    msgapi.A_ALIAS_OPTIONS: 'Alias Options', \
    msgapi.A_LDAP_OPTIONS: 'LDAP Options', \
    msgapi.A_CUSTOM_ALIAS: 'Custom Alias', \
    msgapi.A_ADVERTISING_CONFIG: 'Advertising Config', \
    msgapi.A_LANGUAGE: 'Language', \
    msgapi.A_PREFERENCES: 'Preferences', \
    msgapi.A_QUOTA_WARNING: 'Quota Warning', \
    msgapi.A_QUEUE_TUNING: 'Queue Tuning', \
    msgapi.A_TIMEOUT: 'Timeout', \
    msgapi.A_PRIVACY: 'Privacy', \
    msgapi.A_THREAD_LIMIT: 'Thread Limit', \
    msgapi.A_DBF_PAGESIZE: 'DBF Page Size', \
    msgapi.A_DBF_KEYSIZE: 'DBF Key Size', \
    msgapi.A_ADDRESSBOOK_CONFIG: 'Address Book Configuration', \
    msgapi.A_ADDRESSBOOK_URL_SYSTEM: 'Address Book URL (System)', \
    msgapi.A_ADDRESSBOOK_URL_PUBLIC: 'Address Book URL (Public)', \
    msgapi.A_ADDRESSBOOK: 'Address Book', \
    msgapi.A_SERVER_STANDALONE: 'Server Standalone', \
    msgapi.A_FORWARD_UNDELIVERABLE: 'Forward Undeliverable', \
    msgapi.A_PHRASE: 'Phrase', \
    msgapi.A_ACCOUNTING_ENABLED: 'Accounting Enabled', \
    msgapi.A_ACCOUNTING_DATA: 'Accounting Data', \
    msgapi.A_BILLING_DATA: 'Billing Data', \
    msgapi.A_BLOCKED_ADDRESS: 'Blocked Address', \
    msgapi.A_ALLOWED_ADDRESS: 'Allowed Address', \
    msgapi.A_RECIPIENT_LIMIT: 'Recipient Limit', \
    msgapi.A_RBL_HOST: 'RBL Host', \
    msgapi.A_SIGNATURE: 'Signature', \
    msgapi.A_CONNMGR: 'Connection Manager', \
    msgapi.A_CONNMGR_CONFIG: 'Connection Manager Config', \
    msgapi.A_USER_DOMAIN: 'User Domain', \
    msgapi.A_RTS_ANTISPAM_CONFIG: 'RTS Anti-Spam Config', \
    msgapi.A_SPOOL_DIRECTORY: 'Spool Directory', \
    msgapi.A_SCMS_DIRECTORY: 'SCMS Directory', \
    msgapi.A_DBF_DIRECTORY: 'DBF Directory', \
    msgapi.A_NLS_DIRECTORY: 'NLS Directory', \
    msgapi.A_BIN_DIRECTORY: 'Binary Directory', \
    msgapi.A_LIB_DIRECTORY: 'Library Directory', \
    msgapi.A_DEFAULT_CHARSET: 'Default Character Set', \
    msgapi.A_RULE: 'Rule', \
    msgapi.A_RELAY_DOMAIN: 'Relay Domain', \
    msgapi.A_LIST_DIGESTTIME: 'List Digest Time', \
    msgapi.A_LIST_ABSTRACT: 'List Abstract', \
    msgapi.A_LIST_DESCRIPTION: 'List Description', \
    msgapi.A_LIST_CONFIGURATION: 'List Configuration', \
    msgapi.A_LIST_STORE: 'List Store', \
    msgapi.A_LIST_NMAPSTORE: 'List NMAP Store', \
    msgapi.A_LIST_MODERATOR: 'List Moderator', \
    msgapi.A_LIST_OWNER: 'List Owner', \
    msgapi.A_LIST_SIGNATURE: 'List Signature', \
    msgapi.A_LIST_SIGNATURE_HTML: 'List Signature HTML', \
    msgapi.A_LIST_DIGEST_VERSION: 'List Digest Version', \
    msgapi.A_LIST_MEMBERS: 'List Members', \
    msgapi.A_LISTUSER_OPTIONS: 'List User Options', \
    msgapi.A_LISTUSER_ADMINOPTIONS: 'List User Admin Options', \
    msgapi.A_LISTUSER_PASSWORD: 'List User Password', \
    msgapi.A_FEATURE_SET: 'Feature Set', \
    msgapi.A_PRIVATE_KEY_LOCATION: 'Private Key Location', \
    msgapi.A_CERTIFICATE_LOCATION: 'Certificate Location', \
    msgapi.A_CONFIG_CHANGED: 'Config Changed', \
    msgapi.A_LISTSERVER_NAME: 'List Server Name', \
    msgapi.A_LIST_WELCOME_MESSAGE: 'List Welcome Message', \
    msgapi.A_PARENT_OBJECT: 'Community', \
    msgapi.A_FEATURE_INHERITANCE: 'Feature Inheritance', \
    msgapi.A_TEMPLATE: 'Template', \
    msgapi.A_TIMEZONE: 'Timezone', \
    msgapi.A_LOCALE: 'Locale', \
    msgapi.A_PASSWORD_CONFIGURATION: 'Password Configuration', \
    msgapi.A_TITLE: 'Title', \
    msgapi.A_DEFAULT_TEMPLATE: 'Default Template', \
    msgapi.A_TOM_MANAGER: 'Community Manager', \
    msgapi.A_TOM_CONTEXTS: 'Community Contexts', \
    msgapi.A_TOM_DOMAINS: 'Community Domains', \
    msgapi.A_TOM_OPTIONS: 'Community Options', \
    msgapi.A_DESCRIPTION: 'Description', \
    msgapi.A_STATSERVER_1: 'Statistics Server 1', \
    msgapi.A_STATSERVER_2: 'Statistics Server 2', \
    msgapi.A_STATSERVER_3: 'Statistics Server 3', \
    msgapi.A_STATSERVER_4: 'Statistics Server 4', \
    msgapi.A_STATSERVER_5: 'Statistics Server 5', \
    msgapi.A_STATSERVER_6: 'Statistics Server 6', \
    msgapi.A_NEWS: 'News', \
    msgapi.A_MANAGER: 'Manager', \
    msgapi.A_AVAILABLE_SHARES: 'Available Shares', \
    msgapi.A_OWNED_SHARES: 'Owned Shares', \
    msgapi.A_NEW_SHARE_MESSAGE: 'New Share Message', \
    msgapi.A_AVAILABLE_PROXIES: 'Available Proxies', \
    msgapi.A_OWNED_PROXIES: 'Owned Proxies', \
    msgapi.A_RESOURCE_MANAGER: 'Resource Manager', \
    msgapi.A_BONGO_MESSAGING_SERVER: 'Bongo Messaging Server', \
    msgapi.A_ACL: 'Access Control List', \
    msgapi.A_WA_DEFAULT_TEMPLATE: 'WA Default Template', \
    msgapi.A_WA_ALLOWED_TEMPLATES: 'WA Allowed Templates', \
    msgapi.A_WA_DISALLOWED_TEMPLATES: 'WA Disallowed Templates', \
    msgapi.A_SMS_AUTHORIZED_PHONES: 'SMS Authorized Phones', \
    msgapi.A_CALCMD_ADDRESS_SUFFIX: 'CalCmd Address Suffix', \
    msgapi.A_MAX_LOAD: 'Maximum Load', \
    msgapi.A_ACL_ENABLED: 'ACL Enabled', \
    msgapi.A_UBE_SMTP_AFTER_POP: 'SMTP After POP', \
    msgapi.A_UBE_REMOTE_AUTH_ONLY: 'Remote Sending Requires Authentication', \
    msgapi.A_MAX_FLOOD_COUNT: 'Maximum Flood Count', \
    msgapi.A_MAX_NULL_SENDER_RECIPS: 'Maximum Null-Sender Recipients', \
    msgapi.A_MAX_MX_SERVERS: 'Maximum MX Servers', \
    msgapi.A_RELAY_LOCAL_MAIL: 'Relay Local Mail', \
    msgapi.A_CLUSTERED: 'Clustered', \
    msgapi.A_FORCE_BIND: 'Force Bind', \
    msgapi.A_SSL_TLS: 'SSL and TLS Enabled', \
    msgapi.A_SSL_ALLOW_CHAINED: 'Support Chained/Intermediate Certificates', \
    msgapi.A_BOUNCE_RETURN: 'Return to Sender Enabled', \
    msgapi.A_BOUNCE_CC_POSTMASTER: 'Return to Sender CC Postmaster', \
    msgapi.A_QUOTA_USER: 'User Quota', \
    msgapi.A_QUOTA_SYSTEM: 'System Quota', \
    msgapi.A_REGISTER_QUEUE: 'Register With Queue Number', \
    msgapi.A_QUEUE_LIMIT_BOUNCES: 'Limit bounces to entries per interval', \
    msgapi.A_LIMIT_BOUNCE_ENTRIES: 'Bounce limit entries', \
    msgapi.A_LIMIT_BOUNCE_INTERVAL: 'Bounce limit interval', \
    msgapi.A_DOMAIN_ROUTING: 'Domain Routing', \
    msgapi.A_MONITOR_INTERVAL: 'DMC Monitor Interval', \
    msgapi.A_MANAGER_PORT: 'Manager Port', \
    msgapi.A_MANAGER_SSL_PORT: 'Manager SSL Port', \
    }

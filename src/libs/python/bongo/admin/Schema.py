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

import logging
log = logging.getLogger("bongo.admin.schema")

import os
from xml.dom import minidom, Node


def GenerateSlapd(schemain, schemaout = 'bongo.schema'):
    log.info("writing OpenLDAP schema to %s", os.path.abspath(schemaout))
    schema = SlapdSchema(schemain)
    outfile = open(schemaout, 'w')
    schema.write(outfile)
    outfile.close()

def GenerateAd(schemain, schemaout = 'bongo.ldf',
               prefix='CN=Schema,CN=Configuration,DC=X'):
    log.info("writing Active Directory schema to %s",
             os.path.abspath(schemaout))
    schema = AdSchema(schemain)
    outfile = open(schemaout, 'w')
    schema.write(outfile, prefix)
    outfile.close()

def GenerateEdir(schemain, schemaout = 'bongo.sch'):
    log.info("writing eDirectory schema to %s", os.path.abspath(schemaout))

    schema = EdirSchema(schemain)
    outfile = open(schemaout, 'w')
    schema.write(outfile)
    outfile.close()

def GenerateMdb(schemain, mdb):
    log.info("generating schema through MDB")

    schema = Schema(schemain)
    schema.write(mdb)


class Schema:
    def __init__(self, filename=None):
        self.schemaClasses = []
        self.schemaAttributes = []
        self.classes = {}
        self.attributes = {}
        if filename != None:
            self.read(filename)

    def addClass(self, schemaClass):
        if self.classes.has_key(schemaClass.name):
            self.removeClass(schemaClass.name)
        self.schemaClasses.append(schemaClass)
        self.classes[schemaClass.name] = schemaClass

    def removeClass(self, className):
        self.schemaClasses.remove(self.classes[className])
        del(self.classes[className])

    def getAttribute(self, attrName):
        return self.attributes.get(attrName)

    def addAttribute(self, schemaAttribute):
        if self.attributes.has_key(schemaAttribute.name):
            self.removeAttribute(schemaAttribute.name)
        self.schemaAttributes.append(schemaAttribute)
        self.attributes[schemaAttribute.name] = schemaAttribute

    def removeAttribute(self, attributeName):
        self.schemaAttributes.remove(self.attributes[attributeName])
        del(self.attributes[attributeName])

    def update(self):
        for classNode in self.schemaClasses:
            if classNode.loginClass:
                classNode.allowedAttributes.append('Private Key')
                classNode.allowedAttributes.append('Public Key')
                classNode.allowedAttributes.append('Login Intruder Address')
                classNode.allowedAttributes.append('Login Intruder Attempts')

    def read(self, filename):
        doc = minidom.parse(filename)
        for node in doc.documentElement.childNodes:
            if node.nodeType == Node.ELEMENT_NODE:
                if node.nodeName == 'class':
                    self.addClass(SchemaClass(node))
                elif node.nodeName == 'attribute':
                    self.addAttribute(SchemaAttribute(node))
        self.update()

    def write(self, mdb):
        for attrNode in self.schemaAttributes:
            opts = {}
            opts['type'] = 'string'
            opts['singleval'] = False
            opts['immsync'] = False
            opts['public'] = True
            if attrNode.syntax == 'DistinguishedName':
                opts['type'] = 'dn'
            if attrNode.singleValue:
                opts['singleval'] = True
            if attrNode.syncImmediate:
                opts['immsync'] = True
            mdb.DefineAttribute(attrNode.name, attrNode.oid, opts)
        for classNode in self.schemaClasses:
            opts = {}
            opts['container'] = [classNode.containerClass]
            opts['superclasses'] = classNode.superClasses
            opts['containers'] = classNode.containerClasses
            if classNode.namingAttribute:
                opts['naming'] = [classNode.namingAttribute]
            opts['requires'] = classNode.requiredAttributes
            opts['allows'] = classNode.allowedAttributes
            mdb.DefineClass(classNode.name, classNode.oid, opts)


class SchemaClass:
    def __init__(self, parent):
        self.name = None
        self.oid = None
        self.guid = None
        self.superClasses = []
        self.namingAttribute = None
        self.containerClasses = []
        self.requiredAttributes = []
        self.allowedAttributes = []
        self.effectiveClass = False
        self.containerClass = False
        self.loginClass = False

        attrs = parent.attributes
        for attrName in attrs.keys():
            attrNode = attrs.get(attrName)
            if attrName == 'name':
                self.name = attrNode.nodeValue
            elif attrName == 'oid':
                self.oid = attrNode.nodeValue
            elif attrName == 'guid':
                self.guid = attrNode.nodeValue
            elif attrName == 'super-class':
                self.superClasses.append(attrNode.nodeValue)
            elif attrName == 'naming-attribute':
                self.namingAttribute = attrNode.nodeValue
        for node in parent.childNodes:
            if node.nodeType == Node.ELEMENT_NODE:
                if node.nodeName == 'options':
                    for classNode in node.childNodes:
                        if classNode.nodeType == Node.ELEMENT_NODE and \
                               classNode.nodeName == 'option':
                            attrs = classNode.attributes
                            for attrName in attrs.keys():
                                attrNode = attrs.get(attrName)
                                attrValue = attrNode.nodeValue
                                if attrName == 'type':
                                    if attrValue == 'EffectiveClass':
                                        self.effectiveClass = True
                                    elif attrValue == 'ContainerClass':
                                        self.containerClass = True
                                    elif attrValue == 'LoginClass':
                                        self.loginClass = True
                elif node.nodeName == 'container-classes':
                    for classNode in node.childNodes:
                        if classNode.nodeType == Node.ELEMENT_NODE and \
                               classNode.nodeName == 'class':
                            attrs = classNode.attributes
                            for attrName in attrs.keys():
                                attrNode = attrs.get(attrName)
                                attrValue = attrNode.nodeValue
                                if attrName == 'name':
                                    self.containerClasses.append(attrValue)
                elif node.nodeName == 'required-attributes':
                    for classNode in node.childNodes:
                        if classNode.nodeType == Node.ELEMENT_NODE and \
                               classNode.nodeName == 'attribute':
                            attrs = classNode.attributes
                            for attrName in attrs.keys():
                                attrNode = attrs.get(attrName)
                                attrValue = attrNode.nodeValue
                                if attrName == 'name':
                                    self.requiredAttributes.append(attrValue)
                elif node.nodeName == 'allowed-attributes':
                    for classNode in node.childNodes:
                        if classNode.nodeType == Node.ELEMENT_NODE and \
                               classNode.nodeName == 'attribute':
                            attrs = classNode.attributes
                            for attrName in attrs.keys():
                                attrNode = attrs.get(attrName)
                                attrValue = attrNode.nodeValue
                                if attrName == 'name':
                                    self.allowedAttributes.append(attrValue)


class SchemaAttribute:
    def __init__(self, parent):
        self.name = None
        self.oid = None
    	self.guid = None
        self.syntax = None
        self.syncImmediate = False
        self.singleValue = False

        if not parent:
            return

        attrs = parent.attributes
        for attrName in attrs.keys():
            attrNode = attrs.get(attrName)
            if attrName == 'name':
                self.name = attrNode.nodeValue
            elif attrName == 'oid':
                self.oid = attrNode.nodeValue
            elif attrName == 'guid':
                self.guid = attrNode.nodeValue
            elif attrName == 'syntax':
                self.syntax = attrNode.nodeValue
        for node in parent.childNodes:
            if node.nodeType == Node.ELEMENT_NODE and \
                   node.nodeName == 'options':
                for option in node.childNodes:
                    if option.nodeType == Node.ELEMENT_NODE and \
                           option.nodeName == 'option':
                        attrs = option.attributes
                        for attrName in attrs.keys():
                            attrNode = attrs.get(attrName)
                            attrValue = attrNode.nodeValue
                            if attrName == 'type':
                                if attrValue == 'SyncImmediate':
                                    self.syncImmediate = True
                                elif attrValue == 'SingleValue':
                                    self.singleValue = True

    def __str__(self):
        parts = []
        for part in ("name", "oid", "guid", "syntax", "syncImmediate",
                     "singleValue"):
            parts.append("%s: %s" % (part, self.__dict__.get(part)))

        return "%s (%s)" % (object.__str__(self), ", ".join(parts))

class SlapdSchema(Schema):
    def __init__(self, filename=None):
        Schema.__init__(self, filename)

    def update(self):
        for classNode in self.schemaClasses:
            if classNode.loginClass:
                # add userPassword attribute
                classNode.allowedAttributes.append('userPassword')

            # Fix superclasses
            if 'User' in classNode.superClasses:
                classNode.superClasses.remove('User')
                classNode.superClasses.append('inetOrgPerson')
            if 'Organization' in classNode.superClasses:
                classNode.superClasses.remove('Organization')
                classNode.superClasses.append('organization')
            if 'Organizational Role' in classNode.superClasses:
                classNode.superClasses.remove('Organizational Role')
                classNode.superClasses.append('organizationalRole')
            if 'Organizational Unit' in classNode.superClasses:
                classNode.superClasses.remove('Organizational Unit')
                classNode.superClasses.append('organizationalUnit')
            if 'Group' in classNode.superClasses:
                classNode.superClasses.remove('Group')
                classNode.superClasses.append('groupOfNames')

            # Fix container classes
            if 'User' in classNode.containerClasses:
                classNode.containerClasses.remove('User')
                classNode.containerClasses.append('inetOrgPerson')
            if 'Organization' in classNode.containerClasses:
                classNode.containerClasses.remove('Organization')
                classNode.containerClasses.append('organization')
            if 'Organizational Role' in classNode.containerClasses:
                classNode.containerClasses.remove('Organizational Role')
                classNode.containerClasses.append('organizationalRole')
            if 'Organizational Unit' in classNode.containerClasses:
                classNode.containerClasses.remove('Organizational Unit')
                classNode.containerClasses.append('organizationalUnit')
            if 'Group' in classNode.containerClasses:
                classNode.containerClasses.remove('Group')
                classNode.containerClasses.append('groupOfNames')

    def write(self, outFile):
        # Write schema attributes
        for attrNode in self.schemaAttributes:
            outFile.write('attributetype ( %s NAME \'%s\'\n' % \
                          (attrNode.oid, attrNode.name))
            if attrNode.syntax == 'CaseInsensitiveString':
                outFile.write('\tEQUALITY caseIgnoreIA5Match\n')
                outFile.write('\tSYNTAX 1.3.6.1.4.1.1466.115.121.1.26')
            elif attrNode.syntax == 'DistinguishedName':
                outFile.write('\tEQUALITY distinguishedNameMatch\n')
                outFile.write('\tSYNTAX 1.3.6.1.4.1.1466.115.121.1.12')
            elif attrNode.syntax == 'Binary':
                outFile.write('\tEQUALITY octetStringMatch\n')
                outFile.write('\tSYNTAX 1.3.6.1.4.1.1466.115.121.1.40')
            if attrNode.singleValue:
                outFile.write('\n\tSINGLE-VALUE')
            outFile.write(' )\n')

        # Write schema classes
        for classNode in self.schemaClasses:
            outFile.write('objectclass ( %s NAME \'%s\'' % \
                          (classNode.oid, classNode.name))
            if len(classNode.superClasses) > 0:
                outFile.write('\n\tSUP (')
                for superClass in classNode.superClasses:
                    outFile.write(' \'%s\'' % superClass)
                    outFile.write(' )')
            if not classNode.containerClass:
                outFile.write('\n\tX-NDS_NOT_CONTAINER \'1\'')
            if len(classNode.containerClasses) > 0:
                outFile.write('\n\tX-NDS_CONTAINMENT (')
                for containerClass in classNode.containerClasses:
                    outFile.write(' \'%s\'' % containerClass)
                outFile.write(' )')
            if classNode.namingAttribute:
                outFile.write('\n\tX-NDS_NAMING \'%s\'' % \
                              classNode.namingAttribute)
            if len(classNode.requiredAttributes) > 0:
                outFile.write('\n\tMUST (\n')
                for requiredAttribute in classNode.requiredAttributes:
                    outFile.write('\t\t %s' % requiredAttribute)
                    if id(requiredAttribute) != \
                           id(classNode.requiredAttributes[-1]):
                        outFile.write(' $')
                outFile.write(' )')
            if len(classNode.allowedAttributes) > 0:
                outFile.write('\n\tMAY (\n')
                for allowedAttribute in classNode.allowedAttributes:
                    outFile.write('\t\t %s\n' % allowedAttribute)
                    if id(allowedAttribute) != \
                           id(classNode.allowedAttributes[-1]):
                        outFile.write(' $')
                outFile.write(' )')
            outFile.write(' )\n')


class AdSchema(Schema):
    def __init__(self, filename=None):
        Schema.__init__(self, filename)

    def update(self):
        for classNode in self.schemaClasses:
            if classNode.loginClass:
                # add unicodePwd attribute
                classNode.allowedAttributes.append('unicodePwd')

            # Fix superclasses
            if 'User' in classNode.superClasses:
                classNode.superClasses.remove('User')
                classNode.superClasses.append('inetOrgPerson')
            if 'Organization' in classNode.superClasses:
                classNode.superClasses.remove('Organization')
                classNode.superClasses.append('organization')
            if 'Organizational Role' in classNode.superClasses:
                classNode.superClasses.remove('Organizational Role')
                classNode.superClasses.append('organizationalRole')
            if 'Organizational Unit' in classNode.superClasses:
                classNode.superClasses.remove('Organizational Unit')
                classNode.superClasses.append('organizationalUnit')
            if 'Group' in classNode.superClasses:
                classNode.superClasses.remove('Group')
                classNode.superClasses.append('groupOfNames')

            # Fix container classes
            if 'User' in classNode.containerClasses:
                classNode.containerClasses.remove('User')
                classNode.containerClasses.append('inetOrgPerson')
            if 'Organization' in classNode.containerClasses:
                classNode.containerClasses.remove('Organization')
                classNode.containerClasses.append('organization')
            if 'Organizational Role' in classNode.containerClasses:
                classNode.containerClasses.remove('Organizational Role')
                classNode.containerClasses.append('organizationalRole')
            if 'Organizational Unit' in classNode.containerClasses:
                classNode.containerClasses.remove('Organizational Unit')
                classNode.containerClasses.append('organizationalUnit')
            if 'Group' in classNode.containerClasses:
                classNode.containerClasses.remove('Group')
                classNode.containerClasses.append('groupOfNames')

    def write(self, outFile, prefix='CN=Schema,CN=Configuration,DC=X'):
        # Write schema attributes
        for attrNode in self.schemaAttributes:
            dn = 'cn=' + attrNode.name + ',' + prefix
            outFile.write('dn: %s\r\n' % dn)
            outFile.write('changetype: add\r\n')
            outFile.write('objectClass: attributeSchema\r\n')
            outFile.write('name: %s\r\n' % attrNode.name)
            outFile.write('cn: %s\r\n' % attrNode.name)
            outFile.write('adminDisplayName: %s\r\n' % attrNode.name)
            outFile.write('lDAPDisplayName: %s\r\n' % attrNode.name)
            outFile.write('attributeID: %s\r\n' % attrNode.oid)
            outFile.write('distinguishedName: %s\r\n' % dn)
            if attrNode.syntax == 'DistinguishedName':
                outFile.write('attributeSyntax: 2.5.5.1\r\n')
                outFile.write('oMSyntax: 127\r\n')
            elif attrNode.syntax == 'CaseInsensitiveString':
                outFile.write('attributeSyntax: 2.5.5.5\r\n')
                outFile.write('oMSyntax: 22\r\n')
            elif attrNode.syntax == 'Binary':
                outFile.write('attributeSyntax: 2.5.5.10\r\n')
                outFile.write('oMSyntax: 4\r\n')
            if attrNode.singleValue:
                outFile.write('isSingleValued: TRUE\r\n')
            else:
                outFile.write('isSingleValued: FALSE\r\n')
            outFile.write('objectCategory: CN=Attribute-Schema,%s\r\n' % \
                          prefix)
            outFile.write('schemaIDGUID:: %s\r\n' % attrNode.guid)
            outFile.write('\r\n')

        # Force schema update
        outFile.write('dn:\r\n')
        outFile.write('changetype: modify\r\n')
        outFile.write('add: schemaUpdateNow\r\n')
        outFile.write('schemaUpdateNow: 1\r\n')
        outFile.write('-\r\n')
        outFile.write('\r\n')

        # Write schema classes
        for classNode in self.schemaClasses:
            dn = 'cn=' + classNode.name + ',' + prefix
            outFile.write('dn: %s\r\n' % dn)
            outFile.write('changetype: add\r\n')
            outFile.write('objectClass: classSchema\r\n')
            outFile.write('cn: %s\r\n' % classNode.name)
            outFile.write('adminDisplayName: %s\r\n' % classNode.name)
            outFile.write('ldapDisplayName: %s\r\n' % classNode.name)
            outFile.write('governsID: %s\r\n' % classNode.oid)
            #all classes structural
            outFile.write('objectClassCategory: 1\r\n') 
            outFile.write('objectCategory: CN=Class-Schema,%s\r\n' % \
                          prefix)
            for superClass in classNode.superClasses:
                outFile.write('subClassOf: %s\r\n' % superClass)
            if classNode.namingAttribute:
                outFile.write('rDnAttId: %s\r\n' % \
                              classNode.namingAttribute)
            for containerClass in classNode.containerClasses:
                outFile.write('possSuperiors: %s\r\n' % containerClass)
            for requiredAttribute in classNode.requiredAttributes:
                outFile.write('mustContain: %s\r\n' % requiredAttribute)
            for allowedAttribute in classNode.allowedAttributes:
                outFile.write('mayContain: %s\r\n' % allowedAttribute)
            outFile.write('schemaIDGUID:: %s\r\n' % classNode.guid)
            outFile.write('\r\n')
    
            # Force schema update
            # It might be desirable to add a new attribute to the XML file
            # or otherwise generate this only when it references an
            # internal class or attribute.
            outFile.write('dn:\r\n')
            outFile.write('changetype: modify\r\n')
            outFile.write('add: schemaUpdateNow\r\n')
            outFile.write('schemaUpdateNow: 1\r\n')
            outFile.write('-\r\n')
            outFile.write('\r\n')


class EdirSchema(Schema):
    def __init__(self, filename=None):
        Schema.__init__(self, filename)

    def update(self):
        for classNode in self.schemaClasses:
            if classNode.loginClass:
                # inherit eDirectory login attributes
                classNode.superClasses.append('ndsLoginProperties')

    def write(self, outFile):
        outFile.write('MDBSchemaExtensions DEFINITIONS ::=\n')
        outFile.write('BEGIN\n')

        for attrNode in self.schemaAttributes:
            outFile.write('\"' + attrNode.name + '\" ATTRIBUTE ::=\n')
            outFile.write('{\n')
            outFile.write('\tOperation\tADD')
            if attrNode.syntax == 'DistinguishedName':
                outFile.write(',\n\tSyntaxID\tSYN_DIST_NAME')
            elif attrNode.syntax == 'CaseInsensitiveString':
                outFile.write(',\n\tSyntaxID\tSYN_CI_STRING')
            if attrNode.singleValue or attrNode.syncImmediate:
                outFile.write(',\n\tFlags\t{')
                if attrNode.syncImmediate:
                    outFile.write('DS_SYNC_IMMEDIATE')
                    if attrNode.singleValue:
                        outFile.write(',')
                if attrNode.singleValue:
                    outFile.write('DS_SINGLE_VALUED_ATTR')
                outFile.write('}');
            outFile.write('\n}\n')

        for classNode in self.schemaClasses:
            outFile.write('\"' + classNode.name + '\" OBJECT-CLASS ::=\n')
            outFile.write('{\n')
            outFile.write('\tOperation\tADD')
            if len(classNode.superClasses) > 0:
                outFile.write(',\n\tSubClassOf\t{')
                for spr in classNode.superClasses:
                    outFile.write('\"%s\"' % spr)
                    if id(spr) != id(classNode.superClasses[-1]):
                        outFile.write(',')
                outFile.write('}')
            if classNode.effectiveClass or classNode.containerClass:
                outFile.write(',\n\tFlags\t{')
                if classNode.effectiveClass:
                    outFile.write('DS_EFFECTIVE_CLASS')
                    if classNode.containerClass:
                        outFile.write(',')
                if classNode.containerClass:
                    outFile.write('DS_CONTAINER_CLASS')
                outFile.write('}')
            if len(classNode.containerClasses) > 0:
                outFile.write(',\n\tContainedBy\t{')
                for ctr in classNode.containerClasses:
                    outFile.write('\"%s\"' % ctr)
                    if id(ctr) != id(classNode.containerClasses[-1]):
                        outFile.write(',')
                outFile.write('}')
            if classNode.namingAttribute:
                outFile.write(',\n\tNamedBy\t{\"%s\"}' % \
                              classNode.namingAttribute)
            if len(classNode.requiredAttributes) > 0:
                outFile.write(',\n\tMustContain\t{')
                for att in classNode.requiredAttributes:
                    outFile.write('\"%s\"' % att)
                    if id(att) != id(classNode.requiredAttributes[-1]):
                        outFile.write(',')
                outFile.write('}')
            if len(classNode.allowedAttributes) > 0:
                outFile.write(',\n\tMayContain\t{')
                for att in classNode.allowedAttributes:
                    outFile.write('\"%s\"' % att)
                    if id(att) != id(classNode.allowedAttributes[-1]):
                        outFile.write(',')
                outFile.write('}')
            outFile.write('\n')
            outFile.write('}\n')
        outFile.write('END\n')

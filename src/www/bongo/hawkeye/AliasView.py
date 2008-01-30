import bongo.commonweb
import bongo.commonweb.BongoUtil
from bongo.store.StoreClient import StoreClient, DocTypes
import bongo.external.simplejson as simplejson
from HawkeyeHandler import HawkeyeHandler

storePath = "/config/aliases"

class AliasHandler(HawkeyeHandler):
    def index_GET(self, req, rp):
        global storePath
        # Fetch list of current domains setup
        store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
        store.Store("_system")
        domains = store.List(storePath, ["nmap.document"])
        
        # Make listy
        ret = []
        for domain in domains:
            # Ignore magic .svn file for devel installs (workaround bug in bongo-config)
            if domain.filename.endswith("/.svn") != True:
                obj = { }
                obj["name"] = domain.filename.replace(storePath + "/", "")
                print "Domain was: %s" % domain.filename
                #print "Dir of domain: %s" % dir(domain)
                obj["deletelink"] = "delete/" + domain.uid
                obj["editlink"] = "edit/" + domain.uid
                ret.append(obj)
        
        # Send template foo.
        #self.SetVariable("error", None)
        self.SetVariable("info", None)
        self.SetVariable("domainlist", ret)
        self.SetVariable("systab", "selecteditem")
        store.Quit()
        return self.SendTemplate(req, rp, "index.tpl", title="Domains")

    def index_POST(self, req, rp):
        return bongo.commonweb.HTTP_UNAUTHORIZED
    
    def add_GET(self, req, rp):
        self.SetVariable("newmode", True)
        return self.SendTemplate(req, rp, "edit.tpl", title="Adding new domain")
        
    def add_POST(self, req, rp):
        return self.edit_POST(req, rp, addMode=True)
    
    def edit_GET(self, req, rp):
        global storePath
        store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
        store.Store("_system")
        info = store.Info(rp.path)
        print "Doc info: %s" % info
        #print "Actions on doc info: %s" % dir(info)
        
        print "Doc filename: %s" % info.filename
        
        self.SetVariable("error", None)
        self.SetVariable("dremove", None)
        
        # Template stuff
        #domainName = info.filename.replace(storePath + "/", "")
        domainName = info.filename
        
        # Fetch data and set template variables
        dataobj = store.Read(storePath + "/" + info.filename)
        decoder = simplejson.JSONDecoder()
        data = { }
        try:
            data = decoder.decode(dataobj)
        except ValueError:
            print "Oh darn, JSON parsing failed. Either crap JSON was in the file, or there's nothing to begin with."
        
        if data.has_key("domainalias"):
            domainalias = data["domainalias"]
        else:
            domainalias = None
            
        if data.has_key("aliases"):
            aliases = data["aliases"]
        else:
            aliases = { }
        tpl_aliases = [ ]
        aliases_txt = ""
        
        # Convert aliases to <user>=<domain> list
        for alias in aliases:
            magicstr = "%s=%s" % (alias, aliases[alias])
            tpl_aliases.append(magicstr)
            aliases_txt += (magicstr + ",")
        
        # Mmm, templates
        if domainalias != None and domainalias != "":
            self.SetVariable("domainwide", "checked")
        else:
            self.SetVariable("domainwide", None)
        
        # handle the email->username mapping config
        mapping_value = 0
        if data.has_key("username-mapping"):
            mapping_value = int(data["username-mapping"])
        if mapping_value < 0 or mapping_value > 2:
            mapping_value = 0
        self.SetVariable("username-mapping", mapping_value)
        for i in [0, 1, 2]:
            select = "username-mapping-%d" % i
            if i == mapping_value:
                self.SetVariable(select, "selected")
            else:
                self.SetVariable(select, None)
        
        self.SetVariable("useraliasestxt", aliases_txt)
        self.SetVariable("useraliases", tpl_aliases)
        self.SetVariable("domainalias", domainalias)
        self.SetVariable("name", domainName)
        self.SetVariable("newmode", False)
        store.Quit()
        return self.SendTemplate(req, rp, "edit.tpl", title="Editing " + domainName)
    
    def edit_POST(self, req, rp, addMode=False):
        global storePath
        
        # Get stuff from request URI.
        if addMode:
            # Set later in form loop
            domainGuid = ""
        else:
            domainGuid = rp.path
        
        self.SetVariable("error", None)
        self.SetVariable("info", None)
        self.SetVariable("dremove", None)
        
        config = { }
        
        # Put together some sort of config wha-hoozle to fo-shizzle later.
        form = req.form
        for key in form:
            value = form[key].value
            if key == "name" and addMode:
                print "Name was %s" % value
                domainGuid = value
                rp.path = storePath + "/" + value
                print "new path: %s" % rp.path
            if key == "domainwide":
                # This is the checkbox thingy
                print "Checkbox value returned was: " + value
            elif key == "domainalias":
                # Actual domain alias val
                config["domainalias"] = value
            elif key == "username-mapping":
                # Mapping scheme wanted
                config["username-mapping"] = value
            elif key == "useralias":
                # User aliases
                # Split this, and set array as value.
                # eg we get input of "user=domain,user1=domain2"
                commasplit = value.split(",")
                userlist = { }
                for entry in commasplit:
                    tempy = entry.split("=")
                    if len(tempy) == 2:
                        userlist[tempy[0]] = tempy[1]
                
                print "userlist: %s" % userlist
                config["aliases"] = userlist
        
        # Write the fo-shizzle.
        rp.path = self.SetStoreData(req, domainGuid, config, create=addMode, newcollection=storePath)
        
        # Stop using made up words and sent stuff back to the user to look at.
        if addMode:
            return bongo.commonweb.BongoUtil.redirect(req, "edit/" + rp.path)
        else:
            self.SetVariable("info", "Updated domain successfully.")
            return self.edit_GET(req, rp)
        
    def delete_GET(self, req, rp):
        self.SetVariable("error", None)
        self.SetVariable("info", None)
        self.SetVariable("dremove", None)
        return self.SendTemplate(req, rp, "confirmdelete.tpl", title="Deleting domain")
        
    def delete_POST(self, req, rp):
        # Assume on post that we accept your battle challenge!
        
        try:
            store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
            store.Store("_system")
            store.Delete(rp.path)
            store.Quit()
            self.SetVariable("dremove", "Domain removed successfully.")
        except Exception, e:
            self.SetVariable("error", "Failed to delete domain %s." % rp.path)
        
        return bongo.commonweb.BongoUtil.redirect(req, "../../")

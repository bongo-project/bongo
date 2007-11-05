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
        self.SetVariable("domainlist", ret)
        self.SetVariable("systab", "selecteditem")
        store.Quit()
        return self.SendTemplate(req, rp, "index.tpl", title="Domains")

    def index_POST(self, req, rp):
        return bongo.commonweb.HTTP_UNAUTHORIZED
        
    def edit_GET(self, req, rp):
        global storePath
        store = StoreClient(req.session["credUser"], req.session["credUser"], authPassword=req.session["credPass"])
        store.Store("_system")
        info = store.Info(rp.path)
        #print "Doc info: %s" % info
        #print "Actions on doc info: %s" % dir(info)
        
        # Template stuff
        domainName = info.filename.replace(storePath + "/", "")
        
        # Fetch data and set template variables
        dataobj = store.Read(info.filename)
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
        
        self.SetVariable("useraliasestxt", aliases_txt)
        self.SetVariable("useraliases", tpl_aliases)
        self.SetVariable("domainalias", domainalias)
        self.SetVariable("name", domainName)
        store.Quit()
        return self.SendTemplate(req, rp, "edit.tpl", title="Editing " + domainName)
    
    def edit_POST(self, req, rp):
        global storePath
        
        # Get stuff from request URI.
        domainGuid = rp.path
        
        config = { }
        
        # Put together some sort of config wha-hoozle to fo-shizzle later.
        form = req.form
        for key in form:
            value = form[key].value
            if key == "domainwide":
                # This is the checkbox thingy
                print "Checkbox value returned was: " + value
            elif key == "domainalias":
                # Actual domain alias val
                config["domainalias"] = value
            elif key == "useralias":
                # User aliases
                # Split this, and set array as value.
                # eg we get input of "user=domain,user1=domain2"
                commasplit = value.split(",")
                for entry in commasplit:
                    entry = entry.split("=")
                
                config["aliases"] = commasplit
        
        # Write the fo-shizzle.
        self.SetStoreData(req, storePath, config)
        
        # Stop using made up words and sent stuff back to the user to look at.
        return self.edit_GET(req, rp)
        
    def delete_GET(self, req, rp):
        return self.SendTemplate(req, rp, "confirmdelete.tpl", title="Deleting domain")
        
    def delete_POST(self, req, rp):
        # Assume on post that we accept your battle challenge!
        # TODO: Actually delete file, and return to main domains list.
        return None

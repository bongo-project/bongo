import logging
from bongo import Xpl
from bongo.cmdparse import Command
from bongo.BongoError import BongoError
from bongo.store.StoreClient import DocTypes, StoreClient

class TestRun(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "testrun", aliases=["tr"],
                         summary="Run a test suite against the store",
                         usage="%prog %cmd")

    def Err(self, message):
        print "[ERR] %s" % message

    def Run(self, options, args):
        try:
            store = StoreClient("admin", "admin")
        except:
            self.Err("Couldn't connect to a store for testing.")
            return
        
        # collection testing first
        print "Looking for collections..."
        try:
            colls = store.Collections()
            for coll in colls:
                pass
        except:
            self.Err("Can't list collections (COLLECTIONS)")
        
        print "Adding collection"
        try:
            coll = store.Create("/testcoll", existingOk = True)
        except:
            self.Err("Can't add collection (CREATE)")
        
        print "Listing collection contents"
        try:
            contents = store.List("/testcoll")
            for doc in contents:
                pass
        except:
            self.Err("Can't list collection contents (LIST)")
        
        print "Renaming collection"
        try:
            store.Rename("/testcoll", "/renamed-testcoll")
        except:
            self.Err("Can't rename test collection (RENAME)")
        
        mailpath = Xpl.DEFAULT_DATA_DIR + "/demo/mail"
        mail1 = "%s/%s" % (mailpath, "bongo-general-000")
        mail2 = "%s/%s" % (mailpath, "bongo-general-002")
        
        print "Writing mail to collection..."
        guid = None
        try:
            f = file(mail1)
            guid = store.Write("/renamed-testcoll", DocTypes.Mail, f.read())
            f.close()
        except:
            self.Err("Can't write new doc to renamed-testcoll")
        
        if guid != None:
            print "Removing mail from collection..."
            try:
                store.Delete(guid)
            except:
                self.Err("Can't remove document from renamed-testcoll")
        
        print "Removing collection"
        try:
            store.Remove("/renamed-testcoll")
        except:
            self.Err("Couldn't remove collection (REMOVE)")
        
        
        
        print "Test suite ran successfully."

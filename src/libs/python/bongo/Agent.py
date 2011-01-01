import logging
import time

from bongo.BongoError import BongoError
from libbongo.libs import bongojson, msgapi
from bongo.store.StoreClient import DocTypes, StoreClient

class Agent:
    log = logging.getLogger("Bongo.Error")
    running = True
    store = None
    
    def __init__(self, name='Undefined'):
        self.name = name
        self.log.debug("Starting agent '%s'" % (name))
    
    def connect(self, user, store):
        self.store = StoreClient(user, store, authPassword="bongo", eventCallback=self.callback)
    
    def callback(self, response):
        # we want to override this in the subclass
        self.log.error("Callback received and not handled")
        pass
    
    # without a proper event system in the store, we
    # essentially have to poll. FIXME.
    def waitForEvents(self):
        while self.running:
            self.store.NoOp()
            time.sleep(5)
        
        self.store.Quit()

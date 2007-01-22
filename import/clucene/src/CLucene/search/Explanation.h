/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#ifndef _lucene_search_Explanation
#define _lucene_search_Explanation

#if defined(_LUCENE_PRAGMA_ONCE)
# pragma once
#endif

CL_NS_DEF(search)

   #define LUCENE_SEARCH_EXPLANATION_DESC_LEN 200
   class Explanation :LUCENE_BASE {
   private:
      float_t value;                            // the value of this node
      TCHAR description[LUCENE_SEARCH_EXPLANATION_DESC_LEN];                     // what it represents
      CL_NS(util)::CLArrayList<Explanation*,CL_NS(util)::Deletor::Object<Explanation> > details;                      // sub-explanations

      TCHAR* toString(int32_t depth);
      Explanation(const Explanation& copy);
   public:
      Explanation() {}
      ~Explanation();

      Explanation(float_t value, TCHAR* description);

      Explanation* clone() const;

      /** The value assigned to this explanation node. */
      float_t getValue();
        
      /** Sets the value assigned to this explanation node. */
      void setValue(float_t value);

      /** A description of this explanation node. */
      TCHAR* getDescription(); ///<returns reference
        
      /** Sets the description of this explanation node. */
      void setDescription(TCHAR* description);

      /** The sub-nodes of this explanation node. */
      Explanation** getDetails();

      /** Adds a sub-node to this explanation node. */
      void addDetail(Explanation* detail);

      /** Render an explanation as text. */
      TCHAR* toString();

      /** Render an explanation as HTML. */
      TCHAR* toHtml();
   };

CL_NS_END
#endif

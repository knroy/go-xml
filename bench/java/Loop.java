// Loop is the warm, steady-state harness for the JVM reference engines.
//
//   java -cp <loop dir>:<engine jars> Loop ENGINE TASK COMPILE_INPUT ITEMS_FILE WARMUP ITERATIONS
//
// It compiles COMPILE_INPUT (stylesheet, query or schema) once, then runs every
// item WARMUP times untimed and ITERATIONS times timed. ITEMS_FILE has one item
// per line: the source path (may be empty), then tab-separated name=value
// params. Each item's bytes are read into memory before timing; output is
// serialized to a null stream. Output lines, for benchrun to parse:
//
//   COMPILE <ns>
//   RESULT <item index> <ns>
//   ERROR <item index> <message>      (first failure of an item, on stderr)
//
// Each engine lives in its own nested class so that only the selected
// engine's classes are loaded: the classpath holds one engine's jars.
import java.io.ByteArrayInputStream;
import java.io.File;
import java.io.OutputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

public class Loop {
    interface Runner {
        void run(byte[] src, String systemId, Map<String, String> params) throws Exception;
    }

    public static void main(String[] a) throws Exception {
        if (a.length != 6) {
            System.err.println("usage: Loop ENGINE TASK COMPILE_INPUT ITEMS_FILE WARMUP ITERATIONS");
            System.exit(2);
        }
        String engine = a[0], task = a[1];
        File compile = new File(a[2]);
        int warmup = Integer.parseInt(a[4]), iters = Integer.parseInt(a[5]);

        List<byte[]> srcs = new ArrayList<>();
        List<String> ids = new ArrayList<>();
        List<Map<String, String>> params = new ArrayList<>();
        for (String line : Files.readAllLines(Path.of(a[3]))) {
            String[] f = line.split("\t", -1);
            File s = f[0].isEmpty() ? null : new File(f[0]);
            srcs.add(s == null ? null : Files.readAllBytes(s.toPath()));
            ids.add(s == null ? null : s.toURI().toString());
            Map<String, String> p = new LinkedHashMap<>();
            for (int i = 1; i < f.length; i++) {
                int eq = f[i].indexOf('=');
                if (eq > 0) p.put(f[i].substring(0, eq), f[i].substring(eq + 1));
            }
            params.add(p);
        }

        long t0 = System.nanoTime();
        Runner r = switch (engine + "/" + task) {
            case "saxon-he/xslt" -> SaxonXslt.compile(compile);
            case "saxon-he/xquery" -> SaxonXQuery.compile(compile);
            case "basex/xquery" -> BaseXQuery.compile(compile);
            case "jing/rng" -> JingRng.compile(compile);
            case "xerces-j-xsd11/xsd" -> XercesXsd.compile(compile);
            default -> throw new IllegalArgumentException("no loop for " + engine + "/" + task);
        };
        System.out.println("COMPILE " + (System.nanoTime() - t0));

        boolean[] failed = new boolean[srcs.size()];
        for (int it = 0; it < warmup + iters; it++) {
            for (int i = 0; i < srcs.size(); i++) {
                long s = System.nanoTime();
                try {
                    r.run(srcs.get(i), ids.get(i), params.get(i));
                } catch (Exception e) {
                    if (!failed[i]) {
                        failed[i] = true;
                        System.err.println("ERROR " + i + " " + String.valueOf(e.getMessage()).replace('\n', ' '));
                    }
                }
                long ns = System.nanoTime() - s;
                if (it >= warmup) System.out.println("RESULT " + i + " " + ns);
            }
        }
    }

    static javax.xml.transform.stream.StreamSource source(byte[] b, String id) {
        return new javax.xml.transform.stream.StreamSource(new ByteArrayInputStream(b), id);
    }

    static final class SaxonXslt {
        static Runner compile(File f) throws Exception {
            net.sf.saxon.s9api.Processor p = new net.sf.saxon.s9api.Processor(false);
            net.sf.saxon.s9api.XsltExecutable e = p.newXsltCompiler()
                    .compile(new javax.xml.transform.stream.StreamSource(f));
            return (src, id, params) -> {
                net.sf.saxon.s9api.Xslt30Transformer t = e.load30();
                Map<net.sf.saxon.s9api.QName, net.sf.saxon.s9api.XdmValue> pm = new LinkedHashMap<>();
                params.forEach((k, v) -> pm.put(new net.sf.saxon.s9api.QName(k), new net.sf.saxon.s9api.XdmAtomicValue(v)));
                t.setStylesheetParameters(pm);
                net.sf.saxon.s9api.Serializer out = p.newSerializer(OutputStream.nullOutputStream());
                if (src == null) t.callTemplate(null, out);
                else t.transform(source(src, id), out);
            };
        }
    }

    static final class SaxonXQuery {
        static Runner compile(File f) throws Exception {
            net.sf.saxon.s9api.Processor p = new net.sf.saxon.s9api.Processor(false);
            net.sf.saxon.s9api.XQueryCompiler c = p.newXQueryCompiler();
            c.setBaseURI(f.toURI());
            net.sf.saxon.s9api.XQueryExecutable e = c.compile(f);
            return (src, id, params) -> {
                net.sf.saxon.s9api.XQueryEvaluator ev = e.load();
                if (src != null) ev.setSource(source(src, id));
                for (Map.Entry<String, String> kv : params.entrySet())
                    ev.setExternalVariable(new net.sf.saxon.s9api.QName(kv.getKey()),
                            new net.sf.saxon.s9api.XdmAtomicValue(kv.getValue()));
                ev.run(p.newSerializer(OutputStream.nullOutputStream()));
            };
        }
    }

    // BaseX has no reusable compiled query: a QueryProcessor is parsed and
    // compiled per evaluation. COMPILE is the first parse+compile, and every
    // timed item pays its own compile, which benchrun notes in the results.
    static final class BaseXQuery {
        static Runner compile(File f) throws Exception {
            String q = Files.readString(f.toPath());
            org.basex.core.Context ctx = new org.basex.core.Context();
            try (org.basex.query.QueryProcessor qp = new org.basex.query.QueryProcessor(q, f.toURI().toString(), ctx, null)) {
                qp.compile();
            }
            return (src, id, params) -> {
                try (org.basex.query.QueryProcessor qp = new org.basex.query.QueryProcessor(q, f.toURI().toString(), ctx, null)) {
                    if (src != null) qp.context(new org.basex.query.value.node.DBNode(new org.basex.io.IOContent(src, id)));
                    for (Map.Entry<String, String> kv : params.entrySet()) qp.variable(kv.getKey(), kv.getValue());
                    org.basex.query.iter.Iter iter = qp.iter();
                    try (org.basex.io.serial.Serializer ser = qp.serializer(OutputStream.nullOutputStream())) {
                        for (org.basex.query.value.item.Item item; (item = iter.next()) != null; ) ser.serialize(item);
                    }
                }
            };
        }
    }

    // An invalid document is a result, not a failure: the verdict was already
    // checked against go-xml before timing, so validators discard it here.
    static final class JingRng {
        static Runner compile(File f) throws Exception {
            com.thaiopensource.util.PropertyMapBuilder b = new com.thaiopensource.util.PropertyMapBuilder();
            b.put(com.thaiopensource.validate.ValidateProperty.ERROR_HANDLER, new org.xml.sax.helpers.DefaultHandler());
            com.thaiopensource.validate.ValidationDriver d = new com.thaiopensource.validate.ValidationDriver(b.toPropertyMap());
            if (!d.loadSchema(com.thaiopensource.validate.ValidationDriver.fileInputSource(f)))
                throw new IllegalStateException("schema did not load: " + f);
            return (src, id, params) -> {
                org.xml.sax.InputSource in = new org.xml.sax.InputSource(new ByteArrayInputStream(src));
                in.setSystemId(id);
                d.validate(in);
            };
        }
    }

    static final class XercesXsd {
        static Runner compile(File f) throws Exception {
            javax.xml.validation.SchemaFactory sf = javax.xml.validation.SchemaFactory.newInstance(
                    "http://www.w3.org/XML/XMLSchema/v1.1",
                    "org.apache.xerces.jaxp.validation.XMLSchema11Factory", Loop.class.getClassLoader());
            javax.xml.validation.Schema s = sf.newSchema(f);
            return (src, id, params) -> {
                javax.xml.validation.Validator v = s.newValidator();
                v.setErrorHandler(new org.xml.sax.helpers.DefaultHandler());
                v.validate(source(src, id));
            };
        }
    }
}

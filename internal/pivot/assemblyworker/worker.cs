using System;
using System.IO;
using System.Reflection;
using System.Text;
using System.Threading.Tasks;

// This program is an isolated CLR host for one Undertow assembly run. The
// operator's assembly arrives over stdin and is never written to a file.
internal static class AssemblyWorker
{
    private const int MaxAssemblySize = 8 * 1024 * 1024;
    private const int MaxArgumentBytes = 4096;

    private static int Main()
    {
        try
        {
            Console.OutputEncoding = new UTF8Encoding(false);
            using (var reader = new BinaryReader(Console.OpenStandardInput(), new UTF8Encoding(false)))
            {
                if (Encoding.ASCII.GetString(ReadExact(reader, 4)) != "UTA1")
                    throw new InvalidDataException("Invalid assembly worker request");

                int count = ReadLength(reader, 31);
                var arguments = new string[count];
                int totalArgumentBytes = 0;
                var strictUtf8 = new UTF8Encoding(false, true);
                for (int index = 0; index < count; index++)
                {
                    int length = ReadLength(reader, MaxArgumentBytes - totalArgumentBytes);
                    var bytes = ReadExact(reader, length);
                    arguments[index] = strictUtf8.GetString(bytes);
                    if (arguments[index].IndexOf('\0') >= 0)
                        throw new InvalidDataException("Assembly argument contains NUL");
                    totalArgumentBytes += length;
                }

                int assemblyLength = ReadLength(reader, MaxAssemblySize);
                if (assemblyLength < 256)
                    throw new InvalidDataException("Assembly is too small");
                byte[] source = ReadExact(reader, assemblyLength);
                return Invoke(Assembly.Load(source), arguments);
            }
        }
        catch (Exception error)
        {
            Console.Error.WriteLine(Unwrap(error).ToString());
            return 1;
        }
    }

    private static int ReadLength(BinaryReader reader, int maximum)
    {
        uint value = reader.ReadUInt32();
        if (value > maximum)
            throw new InvalidDataException("Assembly worker request exceeds its limit");
        return (int)value;
    }

    private static byte[] ReadExact(BinaryReader reader, int length)
    {
        byte[] bytes = reader.ReadBytes(length);
        if (bytes.Length != length)
            throw new EndOfStreamException("Truncated assembly worker request");
        return bytes;
    }

    private static int Invoke(Assembly assembly, string[] arguments)
    {
        MethodInfo entry = assembly.EntryPoint;
        if (entry == null)
        {
            const BindingFlags flags = BindingFlags.Static | BindingFlags.Public | BindingFlags.NonPublic;
            foreach (Type type in assembly.GetTypes())
            {
                foreach (MethodInfo method in type.GetMethods(flags))
                {
                    if (method.Name != "Main")
                        continue;
                    if (entry != null)
                        throw new InvalidOperationException("Assembly DLL requires exactly one static Main method");
                    entry = method;
                }
            }
            if (entry == null)
                throw new MissingMethodException("Assembly DLL requires exactly one static Main method");
        }

        ParameterInfo[] parameters = entry.GetParameters();
        object[] invokeArguments;
        if (parameters.Length == 0)
            invokeArguments = new object[0];
        else if (parameters.Length == 1 && parameters[0].ParameterType == typeof(string[]))
            invokeArguments = new object[] { arguments };
        else
            throw new InvalidOperationException("Assembly entry point must be Main() or Main(string[] args)");

        object result = entry.Invoke(null, invokeArguments);
        Task task = result as Task;
        if (task != null)
        {
            task.GetAwaiter().GetResult();
            Type taskType = task.GetType();
            result = taskType.IsGenericType ? taskType.GetProperty("Result").GetValue(task, null) : null;
        }
        return result is int ? (int)result : 0;
    }

    private static Exception Unwrap(Exception error)
    {
        while (error is TargetInvocationException && error.InnerException != null)
            error = error.InnerException;
        return error;
    }
}

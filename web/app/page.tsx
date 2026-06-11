import "./marketing.css";
import TopBar from "@/components/marketing/TopBar";
import Hero from "@/components/marketing/Hero";
import DashboardShot from "@/components/marketing/DashboardShot";
import Features from "@/components/marketing/Features";
import HowItWorks from "@/components/marketing/HowItWorks";
import SessionReplay from "@/components/marketing/SessionReplay";
import CTA from "@/components/marketing/CTA";
import Footer from "@/components/marketing/Footer";

export default function Home() {
  return (
    <div className="rw-marketing">
      <TopBar />
      <main>
        <Hero />
        <SessionReplay />
        <Features />
        <HowItWorks />
        <DashboardShot />
        <CTA />
      </main>
      <Footer />
    </div>
  );
}
